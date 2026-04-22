package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/agent"
	"github.com/cobot-agent/cobot/internal/bootstrap"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

const maxResultDisplayLen = 200

// chatMessage holds both raw and rendered forms of a message.
type chatMessage struct {
	role     string // "user", "assistant", "tool", "error", "system"
	raw      string // raw content (markdown for assistant, plain for others)
	rendered string // glamour-rendered content (only for assistant messages)
}

type tuiModel struct {
	input        textarea.Model
	viewport     viewport.Model
	spinner      spinner.Model
	messages     []chatMessage
	agent        *agent.Agent
	workspace    string // workspace display name
	streaming    bool
	streamCh     <-chan cobot.Event
	streamCancel context.CancelFunc
	pending      []string
	renderer     *glamour.TermRenderer
	width        int
	height       int
	ready        bool
	darkBG       bool
	renderDirty  bool

	hasKeyDisambiguation bool

	// lastTextInput tracks when the last printable text was received,
	// used to debounce Enter after IME composition commits.
	lastTextInput time.Time

	userStyle      lipgloss.Style
	errorStyle     lipgloss.Style
	toolStyle      lipgloss.Style
	statusStyle    lipgloss.Style
	queuedStyle    lipgloss.Style
	hubStyle       lipgloss.Style
	wsMgr          *workspace.Manager
	notificationCh chan cobot.ChannelMessage
	tuiChDone      <-chan struct{}
}

type streamMsg struct {
	content   string
	eventType cobot.EventType
	toolName  string
	done      bool
	err       string
}

type refreshTickMsg struct{}

const refreshInterval = 33 * time.Millisecond // ~30fps

// IME composition commits often send text characters immediately followed by
// an Enter keypress. This threshold skips submit if Enter arrives too soon
// after receiving printable text, preventing accidental submission during
// Chinese/Japanese/Korean input.
const imeDebounceThreshold = 80 * time.Millisecond

func scheduleRefresh() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg {
		return refreshTickMsg{}
	})
}

func newTUIModel(a *agent.Agent, workspaceName string, wsMgr *workspace.Manager) tuiModel {
	ti := textarea.New()
	ti.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ti.ShowLineNumbers = false
	ti.DynamicHeight = true
	ti.MinHeight = 1
	ti.MaxHeight = 8
	ti.CharLimit = 4096
	ti.SetStyles(transparentTextareaStyles())

	ti.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter"),
		key.WithHelp("shift+enter", "new line"),
	)

	ti.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("6"))),
	)

	u, e, t, s, q, h := initStyles()
	return tuiModel{
		input:       ti,
		spinner:     sp,
		agent:       a,
		workspace:   workspaceName,
		messages:    []chatMessage{},
		darkBG:      true,
		userStyle:   u,
		errorStyle:  e,
		toolStyle:   t,
		statusStyle: s,
		queuedStyle: q,
		hubStyle:    h,
		wsMgr:       wsMgr,
	}
}

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick, tea.RequestBackgroundColor}
	if m.notificationCh != nil {
		cmds = append(cmds, pollNotifications(m.notificationCh, m.tuiChDone))
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.darkBG = msg.IsDark()
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		m.hasKeyDisambiguation = msg.SupportsKeyDisambiguation()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.renderer = newGlamourRenderer(m.width)
		m.input.SetWidth(m.width)

		vpHeight := m.height - m.inputHeight()
		if vpHeight < 1 {
			vpHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(vpHeight))
			// Allow scrolling via PageUp/PageDown, but disable single letters
			// like j/k/f/b/d/u/h/l/space that conflict with typing.
			m.viewport.KeyMap = viewport.KeyMap{
				PageDown: key.NewBinding(key.WithKeys("pgdown")),
				PageUp:   key.NewBinding(key.WithKeys("pgup")),
			}
			m.viewport.SetContent(m.renderAllMessages())
			m.ready = true
		} else {
			m.viewport.SetWidth(m.width)
			m.viewport.SetHeight(vpHeight)
			m.viewport.SetContent(m.renderAllMessages())
		}
		return m, nil

	case tea.KeyPressMsg:
		// Track non-ASCII text input for IME debounce.
		// IME composition produces multi-byte characters (CJK, etc.),
		// while regular typing produces single ASCII bytes.
		if msg.Text != "" && msg.Text[0] > 127 {
			m.lastTextInput = time.Now()
		}

		switch msg.String() {
		case "ctrl+c":
			if m.streaming {
				m.finishStream()
				m.messages = append(m.messages, chatMessage{role: "system", raw: "(cancelled)"})
				m.refreshViewport()
				cmd := m.drainPending()
				return m, cmd
			}
			return m, tea.Quit

		case "enter":
			if !m.lastTextInput.IsZero() && time.Since(m.lastTextInput) < imeDebounceThreshold {
				m.lastTextInput = time.Time{}
				break
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if text == "/quit" || text == "/exit" {
				return m, tea.Quit
			}
			// Handle slash commands
			if strings.HasPrefix(text, "/") {
				cmd := m.handleSlashCommand(text)
				return m, cmd
			}
			m.input.Reset()
			if m.streaming {
				m.pending = append(m.pending, text)
				m.messages = append(m.messages, chatMessage{
					role: "user",
					raw:  text + " (queued)",
				})
				m.viewport.GotoBottom()
				m.refreshViewport()
				return m, nil
			}
			cmd := m.startStream(text)
			return m, cmd
		}

	case streamMsg:
		mdl, cmd := m.handleStreamMsg(msg)
		return mdl, cmd

	case spinner.TickMsg:
		if m.streaming {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case refreshTickMsg:
		if m.streaming {
			if m.renderDirty {
				m.refreshViewport()
				m.renderDirty = false
			}
			return m, scheduleRefresh()
		}
		return m, nil

	case notificationMsg:
		m.messages = append(m.messages, chatMessage{
			role: "system",
			raw:  msg.msg.Content,
		})
		m.refreshViewport()
		return m, pollNotifications(m.notificationCh, m.tuiChDone)

	case notificationShutdownMsg:
		// Notification channel closed (TUI exiting), stop polling.
		return m, nil
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m tuiModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing...\n")
	}

	var b strings.Builder

	content := m.renderAllMessages()
	contentLines := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		contentLines++
	}

	availableLines := m.height - m.inputHeight()

	if contentLines <= availableLines {
		// Content fits — render inline without viewport padding
		b.WriteString(content)
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
	} else {
		// Content overflows — use scrollable viewport
		b.WriteString(m.viewport.View())
		b.WriteString("\n")
	}

	// Hub status bar
	b.WriteString(m.renderHub())

	// Status line
	if m.streaming {
		status := m.spinner.View() + m.statusStyle.Render(" Thinking...")
		if len(m.pending) > 0 {
			status += m.statusStyle.Render(fmt.Sprintf(" (%d queued)", len(m.pending)))
		}
		b.WriteString(status)
	}
	b.WriteString("\n")

	b.WriteString(m.input.View())

	v := tea.NewView(b.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// redirectSlogForTUI prevents slog output from corrupting the bubbletea
// alt-screen display. In debug mode, logs go to a file; otherwise they are
// discarded. Returns a cleanup function to close the log file (if any).
func redirectSlogForTUI() func() {
	if debugMode {
		logDir := os.TempDir()
		logPath := filepath.Join(logDir, "cobot-tui.log")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
			return func() { f.Close() }
		}
		// Fall through to discard if file open failed.
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})))
	return func() {}
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Start interactive TUI",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Redirect slog away from stderr before entering alt-screen.
		// Any stderr writes corrupt the bubbletea terminal display.
		cleanupLog := redirectSlogForTUI()
		defer cleanupLog()

		res, err := bootstrap.InitAgent(cfg, false)
		if err != nil {
			return err
		}
		a := res.Agent
		ws := res.Workspace
		cleanup := res.Cleanup
		defer cleanup()

		wsName := ""
		if ws != nil && ws.Definition != nil {
			wsName = ws.Definition.Name
		}

		wsMgr, err := workspace.NewManager()
		if err != nil {
			return err
		}

		// Set up TUI channel for cron notifications
		notifyCh := make(chan cobot.ChannelMessage, 16)
		tuiChannelID := resolveTUIChannelID(cfg)
		tuiCh := newTUIChannel(tuiChannelID, notifyCh)
		tuiSessionID := "tui:" + uuid.NewString()
		if res.ChannelMgr != nil {
			res.ChannelMgr.Register(tuiCh, tuiSessionID)
			res.ChannelMgr.MarkLocal(tuiSessionID)
			defer res.ChannelMgr.Unregister(tuiCh.ID(), tuiSessionID)
			defer tuiCh.Close()
		}

		mdl := newTUIModel(a, wsName, wsMgr)
		mdl.notificationCh = notifyCh
		mdl.tuiChDone = tuiCh.Done()

		// Create a context that cancels on OS signals so Bubble Tea exits gracefully.
		programCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()

		// Run the tea program
		p := tea.NewProgram(mdl, tea.WithContext(programCtx))

		// Run program (blocks until exit)
		finalModel, err := p.Run()
		_ = finalModel // unused but kept for future use
		return err
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// resolveTUIChannelID determines the channel ID for TUI notifications.
// It scans configured channels for the first TUI-type channel, falling back
// to "tui:default" if none is found.
func resolveTUIChannelID(cfg *cobot.Config) string {
	tuiChannelID := "tui:default"
	tuiCount := 0
	for _, ch := range cfg.Channels {
		if ch.Type == "tui" {
			if ch.Name == "" {
				slog.Warn("TUI channel has empty name, skipping")
				continue
			}
			tuiCount++
			if tuiCount == 1 {
				tuiChannelID = fmt.Sprintf("%s:%s", ch.Type, ch.Name)
			}
		}
	}
	if tuiCount > 1 {
		slog.Warn("multiple TUI channels configured, using first", "name", tuiChannelID)
	}
	return tuiChannelID
}

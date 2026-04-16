package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wrap"
	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/agent"
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

	userStyle   lipgloss.Style
	errorStyle  lipgloss.Style
	toolStyle   lipgloss.Style
	statusStyle lipgloss.Style
	queuedStyle lipgloss.Style
	hubStyle    lipgloss.Style
	wsMgr       *workspace.Manager
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

// initStyles creates lipgloss styles.
func initStyles() (user, errSt, tool, status, queued, hub lipgloss.Style) {
	user = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB")).Bold(true)
	errSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	tool = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Faint(true)
	status = lipgloss.NewStyle().Faint(true)
	queued = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Faint(true)
	hub = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Faint(true)
	return
}

func newGlamourRenderer(width int) *glamour.TermRenderer {
	w := width - 2 // leave some margin
	if w < 40 {
		w = 40
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(w),
	)
	if err != nil {
		// Fallback: no markdown rendering if glamour fails
		return nil
	}
	return r
}

func transparentTextareaStyles() textarea.Styles {
	var s textarea.Styles
	s.Focused = textarea.StyleState{
		Base:             lipgloss.NewStyle(),
		CursorLine:       lipgloss.NewStyle(),
		CursorLineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		EndOfBuffer:      lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		LineNumber:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Placeholder:      lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Prompt:           lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Text:             lipgloss.NewStyle(),
	}
	s.Blurred = textarea.StyleState{
		Base:             lipgloss.NewStyle(),
		CursorLine:       lipgloss.NewStyle(),
		CursorLineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		EndOfBuffer:      lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		LineNumber:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Placeholder:      lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Prompt:           lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		Text:             lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	}
	s.Cursor = textarea.CursorStyle{
		Color: lipgloss.Color("15"),
		Shape: tea.CursorBlock,
		Blink: true,
	}
	return s
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
	return tea.Batch(textarea.Blink, m.spinner.Tick, tea.RequestBackgroundColor)
}

// inputHeight returns the number of lines the input area occupies.
func (m tuiModel) inputHeight() int {
	// textarea height + hub line + status line + blank separator
	return m.input.Height() + 3
}

// formatTokenCount formats token counts with k suffix for readability.
func formatTokenCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// renderHub builds the status bar showing model, workspace, and token usage.
func (m tuiModel) renderHub() string {
	usage := m.agent.SessionUsage()
	parts := []string{
		fmt.Sprintf(" ws:%s", m.workspace),
		fmt.Sprintf("model:%s", m.agent.Model()),
		fmt.Sprintf("tok:%s/%s", formatTokenCount(usage.PromptTokens), formatTokenCount(usage.CompletionTokens)),
	}
	if usage.ReasoningTokens > 0 {
		parts = append(parts, fmt.Sprintf("reason:%s", formatTokenCount(usage.ReasoningTokens)))
	}
	if usage.CacheReadTokens > 0 || usage.CacheWriteTokens > 0 {
		parts = append(parts, fmt.Sprintf("cache:%s/%s", formatTokenCount(usage.CacheReadTokens), formatTokenCount(usage.CacheWriteTokens)))
	}
	return m.hubStyle.Render(strings.Join(parts, " │")) + "\n"
}

// handleSlashCommand processes slash commands and returns a tea.Cmd.
func (m *tuiModel) handleSlashCommand(text string) tea.Cmd {
	m.input.Reset()
	parts := strings.SplitN(text, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "/model":
		if arg == "" {
			m.messages = append(m.messages, chatMessage{
				role: "system", raw: fmt.Sprintf("Current model: %s", m.agent.Model()),
			})
			break
		}
		if err := m.agent.SetModel(arg); err != nil {
			m.messages = append(m.messages, chatMessage{
				role: "error", raw: fmt.Sprintf("Failed to set model: %v", err),
			})
		} else {
			m.messages = append(m.messages, chatMessage{
				role: "system", raw: fmt.Sprintf("Model switched to: %s", m.agent.Model()),
			})
		}

	case "/usage":
		u := m.agent.SessionUsage()
		info := fmt.Sprintf("Session usage — input: %d, output: %d, total: %d", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
		if u.ReasoningTokens > 0 {
			info += fmt.Sprintf(", reasoning: %d", u.ReasoningTokens)
		}
		if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
			info += fmt.Sprintf(", cache_read: %d, cache_write: %d", u.CacheReadTokens, u.CacheWriteTokens)
		}
		m.messages = append(m.messages, chatMessage{
			role: "system",
			raw:  info,
		})

	case "/reset":
		m.agent.ResetUsage()
		m.messages = append(m.messages, chatMessage{role: "system", raw: "Usage counters reset."})

	case "/workspace":
		if m.wsMgr == nil {
			m.messages = append(m.messages, chatMessage{
				role: "error", raw: "Workspace manager not available.",
			})
			break
		}
		if arg == "" || arg == "list" {
			defs, err := m.wsMgr.List()
			if err != nil {
				m.messages = append(m.messages, chatMessage{
					role: "error", raw: fmt.Sprintf("Failed to list workspaces: %v", err),
				})
				break
			}
			if len(defs) == 0 {
				m.messages = append(m.messages, chatMessage{
					role: "system", raw: "No workspaces found.",
				})
				break
			}
			var lines []string
			for _, d := range defs {
				lines = append(lines, fmt.Sprintf("  %s (%s)", d.Name, d.Type))
			}
			m.messages = append(m.messages, chatMessage{
				role: "system", raw: "Workspaces:\n" + strings.Join(lines, "\n"),
			})
		} else {
			ws, err := m.wsMgr.Resolve(arg)
			if err != nil {
				m.messages = append(m.messages, chatMessage{
					role: "error", raw: fmt.Sprintf("Failed to resolve workspace: %v", err),
				})
				break
			}
			if err := reconfigureAgentForWorkspace(m.agent, ws, m.agent.Registry()); err != nil {
				m.messages = append(m.messages, chatMessage{
					role: "error", raw: fmt.Sprintf("Failed to switch workspace: %v", err),
				})
				break
			}
			m.workspace = ws.Definition.Name
			m.messages = append(m.messages, chatMessage{
				role: "system", raw: fmt.Sprintf("Switched to workspace: %s", ws.Definition.Name),
			})
		}

	case "/help":
		m.messages = append(m.messages, chatMessage{
			role: "system",
			raw:  "Commands: /model <spec>  /usage  /reset  /workspace [list|<name>]  /help  /quit",
		})

	default:
		m.messages = append(m.messages, chatMessage{
			role: "error", raw: fmt.Sprintf("Unknown command: %s  (try /help)", cmd),
		})
	}

	m.viewport.GotoBottom()
	m.refreshViewport()
	return nil
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
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) startStream(text string) tea.Cmd {
	m.messages = append(m.messages, chatMessage{role: "user", raw: text})
	m.streaming = true
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	ch, err := m.agent.Stream(ctx, text)
	if err != nil {
		cancel()
		return func() tea.Msg { return streamMsg{err: err.Error()} }
	}
	m.streamCh = ch
	m.viewport.GotoBottom()
	m.refreshViewport()
	return tea.Batch(m.readNextEvent(), m.spinner.Tick, scheduleRefresh())
}

func (m *tuiModel) finishStream() {
	m.streaming = false
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	m.streamCh = nil
}

func (m *tuiModel) drainPending() tea.Cmd {
	if len(m.pending) == 0 {
		return nil
	}
	next := m.pending[0]
	m.pending = m.pending[1:]
	return m.startStream(next)
}

func (m tuiModel) readNextEvent() tea.Cmd {
	ch := m.streamCh
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return streamMsg{done: true}
		}
		sm := streamMsg{
			content:   evt.Content,
			eventType: evt.Type,
			done:      evt.Done,
			err:       evt.Error,
		}
		if evt.ToolCall != nil {
			sm.toolName = evt.ToolCall.Name
		}
		return sm
	}
}

func (m tuiModel) handleStreamMsg(msg streamMsg) (tea.Model, tea.Cmd) {
	// Tool errors (EventToolResult with Error) are non-fatal — the agent loop
	// feeds them back to the model for retry. Only EventError is terminal.
	if msg.err != "" && msg.eventType != cobot.EventToolResult {
		m.finishStream()
		m.messages = append(m.messages, chatMessage{role: "error", raw: msg.err})
		m.renderLastAssistant()
		m.refreshViewport()
		cmd := m.drainPending()
		return m, cmd
	}
	if msg.done {
		m.finishStream()
		m.renderLastAssistant()
		m.refreshViewport()
		cmd := m.drainPending()
		return m, cmd
	}

	switch msg.eventType {
	case cobot.EventText:
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].raw += msg.content
		} else {
			m.messages = append(m.messages, chatMessage{role: "assistant", raw: msg.content})
		}
	case cobot.EventToolCall:
		m.messages = append(m.messages, chatMessage{role: "tool", raw: fmt.Sprintf("[Tool: %s]", msg.toolName)})
	case cobot.EventToolResult:
		short := msg.content
		if len(short) > maxResultDisplayLen {
			short = short[:maxResultDisplayLen] + "..."
		}
		prefix := "Result"
		if msg.err != "" {
			prefix = "Error"
		}
		m.messages = append(m.messages, chatMessage{role: "tool", raw: fmt.Sprintf("[%s: %s]", prefix, short)})
	case cobot.EventError:
		m.messages = append(m.messages, chatMessage{role: "error", raw: msg.content})
	}

	m.renderDirty = true
	return m, m.readNextEvent()
}

// renderLastAssistant finds the last assistant message and renders it through glamour.
func (m *tuiModel) renderLastAssistant() {
	if m.renderer == nil {
		return
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" && m.messages[i].rendered == "" {
			rendered, err := m.renderer.Render(m.messages[i].raw)
			if err == nil {
				m.messages[i].rendered = strings.TrimRight(rendered, "\n")
			}
			return
		}
	}
}

// refreshViewport rebuilds the viewport content from all messages.
func (m *tuiModel) refreshViewport() {
	if !m.ready {
		return
	}

	atBottom := m.viewport.AtBottom() || m.viewport.YOffset() == 0

	content := m.renderAllMessages()
	m.viewport.SetContent(content)

	if atBottom {
		m.viewport.GotoBottom()
	}
}

// renderAllMessages produces the full rendered output for the viewport.
func (m tuiModel) renderAllMessages() string {
	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(m.userStyle.Render("> "+msg.raw) + "\n")
		case "assistant":
			if msg.rendered != "" {
				// Use glamour-rendered markdown
				b.WriteString(msg.rendered + "\n")
			} else {
				// Still streaming — show raw text, wrapped to prevent TUI hangs on long lines
				w := m.width - 2
				if w < 10 {
					w = 10
				}
				wrapped := wrap.String(msg.raw, w)
				b.WriteString(wrapped)
				if len(wrapped) > 0 && !strings.HasSuffix(wrapped, "\n") {
					b.WriteString("\n")
				}
			}
		case "tool":
			b.WriteString(m.toolStyle.Render("  "+msg.raw) + "\n")
		case "error":
			b.WriteString(m.errorStyle.Render("Error: "+msg.raw) + "\n")
		case "system":
			b.WriteString(m.statusStyle.Render(msg.raw) + "\n")
		}
	}
	return b.String()
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

		a, ws, cleanup, err := initAgent(cfg, false)
		if err != nil {
			return err
		}
		defer cleanup()

		wsName := ""
		if ws != nil && ws.Definition != nil {
			wsName = ws.Definition.Name
		}

		wsMgr, err := workspace.NewManager()
		if err != nil {
			return err
		}

		p := tea.NewProgram(
			newTUIModel(a, wsName, wsMgr),
			tea.WithContext(context.Background()),
		)
		_, err = p.Run()
		return err
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

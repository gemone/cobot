package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cobot-agent/cobot/internal/channel"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Config holds gateway server settings.
type Config struct {
	Addr string
}

// MessageHandler processes inbound messages and replies via the provided ReplyFunc.
type MessageHandler func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error

// ReplyFunc sends an outbound message through the originating channel.
type ReplyFunc func(msg *cobot.OutboundMessage) (*cobot.SendResult, error)

// Gateway is a long-running HTTP server that bridges external platforms to the agent.
// It registers MessageChannel instances, mounts their webhook handlers,
// routes inbound messages through dedup to the MessageHandler, and exposes
// a REST API for dynamic channel registration.
type Gateway struct {
	server     *http.Server
	mux        *http.ServeMux
	channelMgr *channel.Manager
	handler    MessageHandler

	// dedup tracks processed message IDs to prevent duplicate handling.
	dedup          map[string]time.Time
	dedupMu        sync.Mutex
	dedupLastPrune time.Time

	// reverse tracks dynamically registered reverse channels.
	reverse   map[string]*reverseEntry
	reverseMu sync.Mutex

	// registerReverseFunc creates ReverseChannel instances via API.
	registerReverseFunc RegisterReverseFunc

	// lifecycle
	listener   net.Listener
	listenerMu sync.Mutex
	started    bool
	mu         sync.RWMutex
}

type reverseEntry struct {
	ch          cobot.MessageChannel
	callbackURL string
}

// New creates a Gateway wired to the given ChannelManager and MessageHandler.
func New(cfg Config, channelMgr *channel.Manager, handler MessageHandler) *Gateway {
	addr := cfg.Addr
	if addr == "" {
		addr = ":8080"
	}
	mux := http.NewServeMux()
	gw := &Gateway{
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		mux:        mux,
		channelMgr: channelMgr,
		handler:    handler,
		dedup:      make(map[string]time.Time),
		reverse:    make(map[string]*reverseEntry),
	}
	mux.HandleFunc("/health", gw.handleHealth)
	mux.HandleFunc("/api/v1/channels", gw.handleChannels)
	mux.HandleFunc("/api/v1/channels/", gw.handleChannelMessages)
	return gw
}

// RegisterChannel registers a MessageChannel with the Gateway.
// It wires OnMessage → dedup → handler → SendMessage, and if the channel
// implements HTTPChannel, mounts its webhook handler at /webhook/{id}/.
func (g *Gateway) RegisterChannel(ch cobot.MessageChannel) error {
	id := ch.ID()
	if id == "" {
		return fmt.Errorf("channel ID must not be empty")
	}
	// Validate ID is a safe URL path segment.
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == ':') {
			return fmt.Errorf("channel ID must contain only lowercase alphanumeric, hyphens, underscores, and colons")
		}
	}

	// Wire OnMessage → dedup → agent handler.
	ch.OnMessage(func(ctx context.Context, msg *cobot.InboundMessage) {
		if msg.MessageID != "" {
			dedupKey := id + ":" + msg.MessageID
			if !g.recordDedup(dedupKey) {
				slog.Debug("gateway: skipping duplicate message", "channel", id, "message_id", msg.MessageID)
				return
			}
		} else {
			slog.Debug("gateway: message has no MessageID, skipping dedup", "channel", id)
		}

		replyFunc := func(out *cobot.OutboundMessage) (*cobot.SendResult, error) {
			if out.ReceiveID == "" {
				out.ReceiveID = msg.ChatID
			}
			if out.ReceiveType == "" {
				out.ReceiveType = msg.ChatType
			}
			return ch.SendMessage(ctx, out)
		}

		if g.handler != nil {
			if err := g.handler(ctx, msg, replyFunc); err != nil {
				slog.Error("gateway: message handler error", "channel", id, "error", err)
			}
		}
	})

	// Mount webhook if HTTPChannel.
	if hc, ok := ch.(cobot.HTTPChannel); ok {
		pattern := "/webhook/" + id + "/"
		g.mux.Handle(pattern, http.StripPrefix("/webhook/"+id, hc.HTTPHandler()))
		slog.Info("gateway: mounted webhook", "channel", id, "pattern", pattern)
	}

	// Register in ChannelManager.
	g.channelMgr.Register(ch, "gateway:"+id)
	slog.Info("gateway: channel registered", "channel", id, "platform", ch.Platform())
	return nil
}

// Start starts the HTTP server (non-blocking).
func (g *Gateway) Start() error {
	g.listenerMu.Lock()
	if g.started {
		g.listenerMu.Unlock()
		return fmt.Errorf("gateway already started")
	}
	ln, err := net.Listen("tcp", g.server.Addr)
	if err != nil {
		g.listenerMu.Unlock()
		return fmt.Errorf("gateway listen: %w", err)
	}
	g.listener = ln
	g.started = true
	g.listenerMu.Unlock()

	slog.Info("gateway: starting server", "addr", ln.Addr().String())
	go func() {
		if err := g.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway: server error", "error", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server and disconnects all registered channels.
func (g *Gateway) Shutdown(ctx context.Context) error {
	slog.Info("gateway: shutting down")
	if err := g.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("gateway shutdown: %w", err)
	}
	return nil
}

// Addr returns the server's listen address (valid after Start).
func (g *Gateway) Addr() string {
	g.listenerMu.Lock()
	listener := g.listener
	g.listenerMu.Unlock()
	if listener != nil {
		return listener.Addr().String()
	}
	return g.server.Addr
}

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (g *Gateway) recordDedup(key string) bool {
	g.dedupMu.Lock()
	defer g.dedupMu.Unlock()
	now := time.Now()
	if now.Sub(g.dedupLastPrune) > time.Minute {
		for k, t := range g.dedup {
			if now.Sub(t) > 5*time.Minute {
				delete(g.dedup, k)
			}
		}
		g.dedupLastPrune = now
	}
	if _, exists := g.dedup[key]; exists {
		return false
	}
	g.dedup[key] = now
	return true
}

// channelsResponse is the JSON response for GET /api/v1/channels.
type channelsResponse struct {
	Channels []channelInfo `json:"channels"`
}

type channelInfo struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Webhook  bool   `json:"webhook"`
}

// handleChannels handles GET (list) and POST (register reverse) on /api/v1/channels.
func (g *Gateway) handleChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.listChannels(w, r)
	case http.MethodPost:
		g.registerReverseChannel(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *Gateway) listChannels(w http.ResponseWriter, r *http.Request) {
	ids := g.channelMgr.AllAliveIDs()
	channels := make([]channelInfo, 0, len(ids))
	for _, id := range ids {
		ch, ok := g.channelMgr.Get(id)
		if !ok {
			continue
		}
		ci := channelInfo{ID: id}
		if mc, ok := ch.(cobot.MessageChannel); ok {
			ci.Platform = mc.Platform()
			if _, ok := mc.(cobot.HTTPChannel); ok {
				ci.Webhook = true
			}
		}
		channels = append(channels, ci)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channelsResponse{Channels: channels})
}

type registerRequest struct {
	ID          string `json:"id"`
	CallbackURL string `json:"callback_url"`
	Secret      string `json:"secret,omitempty"`
}

func (g *Gateway) registerReverseChannel(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID == "" || req.CallbackURL == "" {
		http.Error(w, "id and callback_url are required", http.StatusBadRequest)
		return
	}

	// Create ReverseChannel via a factory function that the bootstrap sets.
	// For now, we use a simple approach — the gateway creates it directly.
	// The actual ReverseChannel type is in internal/channel, imported via a factory.
	// We'll handle this via a RegisterReverseFunc set during New().
	if g.registerReverseFunc == nil {
		http.Error(w, "reverse channel registration not configured", http.StatusNotImplemented)
		return
	}

	ch, err := g.registerReverseFunc(req.ID, req.CallbackURL, req.Secret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := g.RegisterChannel(ch); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":      ch.ID(),
		"status":  "registered",
		"webhook": "/webhook/" + ch.ID() + "/",
	})
}

// handleChannelMessages handles /api/v1/channels/{id}/messages.
func (g *Gateway) handleChannelMessages(w http.ResponseWriter, r *http.Request) {
	// Extract channel ID from path: /api/v1/channels/{id}/messages
	path := r.URL.Path
	// Strip "/api/v1/channels/" prefix
	remainder := path[len("/api/v1/channels/"):]
	// Find the next "/" to separate channel ID
	idx := len(remainder)
	if slashIdx := indexByte(remainder, '/'); slashIdx >= 0 {
		idx = slashIdx
	}
	channelID := remainder[:idx]
	subPath := remainder[idx:]

	switch {
	case subPath == "" && r.Method == http.MethodDelete:
		g.unregisterChannel(w, r, channelID)
	case subPath == "/messages" && r.Method == http.MethodPost:
		g.sendChannelMessage(w, r, channelID)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (g *Gateway) unregisterChannel(w http.ResponseWriter, r *http.Request, channelID string) {
	ch, ok := g.channelMgr.Get(channelID)
	if !ok {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	g.channelMgr.Unregister(channelID, "gateway:"+channelID)
	ch.Close()
	w.WriteHeader(http.StatusNoContent)
}

type sendMessageRequest struct {
	ChatID   string `json:"chat_id"`
	ChatType string `json:"chat_type,omitempty"`
	Text     string `json:"text"`
}

func (g *Gateway) sendChannelMessage(w http.ResponseWriter, r *http.Request, channelID string) {
	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	ch, ok := g.channelMgr.Get(channelID)
	if !ok {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	mc, ok := ch.(cobot.MessageChannel)
	if !ok {
		http.Error(w, "channel does not support messaging", http.StatusBadRequest)
		return
	}

	// Create an InboundMessage and route through the handler.
	msg := &cobot.InboundMessage{
		Platform: mc.Platform(),
		ChatID:   req.ChatID,
		ChatType: req.ChatType,
		Text:     req.Text,
	}

	// Collect the reply synchronously.
	var reply *cobot.OutboundMessage
	replyFunc := func(out *cobot.OutboundMessage) (*cobot.SendResult, error) {
		reply = out
		return &cobot.SendResult{Success: true}, nil
	}

	if err := g.handler(r.Context(), msg, replyFunc); err != nil {
		http.Error(w, fmt.Sprintf("handler error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if reply != nil {
		json.NewEncoder(w).Encode(map[string]string{"reply": reply.Text})
	} else {
		json.NewEncoder(w).Encode(map[string]string{"reply": ""})
	}
}

// RegisterReverseFunc is set by bootstrap to create ReverseChannel instances.
// This decouples gateway from the channel package's concrete types.
type RegisterReverseFunc func(id, callbackURL, secret string) (cobot.MessageChannel, error)

// SetRegisterReverseFunc sets the factory for creating reverse channels via API.
func (g *Gateway) SetRegisterReverseFunc(fn RegisterReverseFunc) {
	g.registerReverseFunc = fn
}

// indexByte returns the index of byte c in s, or -1 if not found.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

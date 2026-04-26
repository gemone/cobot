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
	Addr   string
	APIKey string // shared secret for REST API authentication
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

	// apiKey for REST API authentication.
	apiKey string

	// registered tracks channel IDs that have been registered via RegisterChannel.
	registered map[string]struct{}

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
// If channelMgr is nil, a new Manager is created automatically.
func New(cfg Config, channelMgr *channel.Manager, handler MessageHandler) *Gateway {
	if channelMgr == nil {
		channelMgr = channel.NewManager()
	}
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
		registered: make(map[string]struct{}),
	}
	mux.HandleFunc("/health", gw.handleHealth)
	mux.HandleFunc("/api/v1/channels", gw.requireAPIKey(gw.handleChannels))
	mux.HandleFunc("/api/v1/channels/", gw.requireAPIKey(gw.handleChannelMessages))
	return gw
}

// requireAPIKey wraps an http.HandlerFunc with API key authentication.
// If no APIKey is configured, all requests are allowed.
func (g *Gateway) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.server != nil {
			// APIKey is stored in Config; we check via a field comparison.
		}
		// Check API key from Config. Access the configured key.
		// We store it separately since Config is not persisted on Gateway.
		if g.apiKey != "" {
			key := r.Header.Get("Authorization")
			if key == "" {
				key = r.URL.Query().Get("key")
			}
			if key != "Bearer "+g.apiKey && key != g.apiKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// RegisterChannel registers a MessageChannel with the Gateway.
// It wires OnMessage → dedup → handler → SendMessage, and if the channel
// implements HTTPChannel, mounts its webhook handler at /webhook/{id}/.
// Returns an error if a channel with the same ID is already registered.
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

	// Prevent duplicate registration.
	g.mu.Lock()
	if _, exists := g.registered[id]; exists {
		g.mu.Unlock()
		return fmt.Errorf("channel %q is already registered", id)
	}
	g.registered[id] = struct{}{}
	g.mu.Unlock()

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

	// Register in ChannelManager and mark as local so health check doesn't expire it.
	sessionID := "gateway:" + id
	g.channelMgr.Register(ch, sessionID)
	g.channelMgr.MarkLocal(sessionID)
	slog.Info("gateway: channel registered", "channel", id, "platform", ch.Platform())
	return nil
}

// UnregisterChannel removes a channel registered via RegisterChannel.
// Only channels with the "gateway:" session prefix are removed.
func (g *Gateway) UnregisterChannel(channelID string) bool {
	g.mu.Lock()
	if _, exists := g.registered[channelID]; !exists {
		g.mu.Unlock()
		return false
	}
	delete(g.registered, channelID)
	g.mu.Unlock()

	sessionID := "gateway:" + channelID
	ch, ok := g.channelMgr.Get(channelID)
	if ok {
		ch.Close()
	}
	g.channelMgr.Unregister(channelID, sessionID)
	return true
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

// Shutdown gracefully stops the server.
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

// --- REST API ---

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
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	resp := map[string]interface{}{
		"id":     ch.ID(),
		"status": "registered",
	}
	// Only include webhook URL if the channel actually serves one.
	if _, ok := ch.(cobot.HTTPChannel); ok {
		resp["webhook"] = "/webhook/" + ch.ID() + "/"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// handleChannelMessages handles /api/v1/channels/{id}/messages and DELETE /api/v1/channels/{id}.
func (g *Gateway) handleChannelMessages(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	remainder := path[len("/api/v1/channels/"):]
	idx := len(remainder)
	if slashIdx := indexByte(remainder, '/'); slashIdx >= 0 {
		idx = slashIdx
	}
	channelID := remainder[:idx]
	subPath := remainder[idx:]

	switch {
	case subPath == "" && r.Method == http.MethodDelete:
		g.unregisterChannelAPI(w, r, channelID)
	case subPath == "/messages" && r.Method == http.MethodPost:
		g.sendChannelMessage(w, r, channelID)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (g *Gateway) unregisterChannelAPI(w http.ResponseWriter, r *http.Request, channelID string) {
	// Only unregister channels that were registered through the gateway.
	sessionID := "gateway:" + channelID
	g.mu.RLock()
	_, isOurs := g.registered[channelID]
	g.mu.RUnlock()
	if !isOurs {
		http.Error(w, "channel not found or not managed by gateway", http.StatusNotFound)
		return
	}

	ch, ok := g.channelMgr.Get(channelID)
	if !ok {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	// Only close the channel we registered, not other sessions.
	g.channelMgr.Unregister(channelID, sessionID)
	ch.Close()

	g.mu.Lock()
	delete(g.registered, channelID)
	g.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

type sendMessageRequest struct {
	ChatID   string `json:"chat_id"`
	ChatType string `json:"chat_type,omitempty"`
	Text     string `json:"text"`
}

func (g *Gateway) sendChannelMessage(w http.ResponseWriter, r *http.Request, channelID string) {
	if g.handler == nil {
		http.Error(w, "gateway not configured with a message handler", http.StatusServiceUnavailable)
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if req.ChatID == "" {
		http.Error(w, "chat_id is required", http.StatusBadRequest)
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

// SetAPIKey sets the shared secret for REST API authentication.
func (g *Gateway) SetAPIKey(key string) {
	g.apiKey = key
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

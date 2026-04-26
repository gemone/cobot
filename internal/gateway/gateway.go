package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type Config struct {
	Addr string
}

type MessageHandler func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error

type ReplyFunc func(msg *cobot.OutboundMessage) (*cobot.SendResult, error)

type Gateway struct {
	server     *http.Server
	mux        *http.ServeMux
	adapters   map[string]cobot.PlatformAdapter
	handler    MessageHandler
	listener   net.Listener
	listenerMu sync.Mutex
	started    bool

	dedup          map[string]time.Time
	dedupMu        sync.Mutex
	dedupLastPrune time.Time

	mu sync.RWMutex
}

func New(cfg Config, handler MessageHandler) *Gateway {
	addr := cfg.Addr
	if addr == "" {
		addr = ":8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	return &Gateway{
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		mux:      mux,
		adapters: make(map[string]cobot.PlatformAdapter),
		handler:  handler,
		dedup:    make(map[string]time.Time),
	}
}

func (g *Gateway) RegisterAdapter(adapter cobot.PlatformAdapter) error {
	platform := adapter.Platform()
	if platform == "" {
		return fmt.Errorf("adapter platform name must not be empty")
	}
	for _, c := range platform {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("platform name must contain only lowercase alphanumeric, hyphens, and underscores")
		}
	}
	g.mu.Lock()
	if _, exists := g.adapters[platform]; exists {
		g.mu.Unlock()
		return fmt.Errorf("adapter for platform %q already registered", platform)
	}

	g.adapters[platform] = adapter
	g.mu.Unlock()

	adapter.OnMessage(func(ctx context.Context, msg *cobot.InboundMessage) {
		if msg.MessageID == "" {
			slog.Debug("gateway: message has no MessageID, skipping dedup", "platform", msg.Platform)
		} else {
			dedupKey := platform + ":" + msg.MessageID
			if !g.recordDedup(dedupKey) {
				slog.Debug("gateway: skipping duplicate message", "platform", msg.Platform, "message_id", msg.MessageID)
				return
			}
		}

		replyFunc := func(out *cobot.OutboundMessage) (*cobot.SendResult, error) {
			if out.ReceiveID == "" {
				out.ReceiveID = msg.ChatID
			}
			if out.ReceiveType == "" {
				out.ReceiveType = msg.ChatType
			}
			return adapter.Send(ctx, out)
		}

		if g.handler != nil {
			if err := g.handler(ctx, msg, replyFunc); err != nil {
				slog.Error("gateway: message handler error", "platform", msg.Platform, "error", err)
			}
		}
	})

	handler, err := adapter.Connect()
	if err != nil {
		g.mu.Lock()
		delete(g.adapters, platform)
		g.mu.Unlock()
		return fmt.Errorf("adapter %q connect failed: %w", platform, err)
	}
	if handler != nil {
		pattern := "/webhook/" + platform + "/"
		g.mux.Handle(pattern, http.StripPrefix("/webhook/"+platform, handler))
		slog.Info("gateway: registered webhook route", "platform", platform, "pattern", pattern)
	}

	g.mu.Lock()
	g.adapters[platform] = adapter
	g.mu.Unlock()
	slog.Info("gateway: adapter registered", "platform", platform)
	return nil
}

func (g *Gateway) Start() error {
	g.listenerMu.Lock()
	if g.started {
		g.listenerMu.Unlock()
		return fmt.Errorf("gateway already started")
	}
	g.started = true
	ln, err := net.Listen("tcp", g.server.Addr)
	if err != nil {
		g.listenerMu.Unlock()
		return fmt.Errorf("gateway listen: %w", err)
	}
	g.listener = ln
	g.listenerMu.Unlock()
	slog.Info("gateway: starting server", "addr", ln.Addr().String())
	go func() {
		if err := g.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("gateway: server error", "error", err)
		}
	}()
	return nil
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	slog.Info("gateway: shutting down")
	if err := g.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("gateway shutdown: %w", err)
	}
	g.mu.RLock()
	adapters := make(map[string]cobot.PlatformAdapter, len(g.adapters))
	for k, v := range g.adapters {
		adapters[k] = v
	}
	g.mu.RUnlock()
	for platform, adapter := range adapters {
		if err := adapter.Disconnect(); err != nil {
			slog.Warn("gateway: adapter disconnect error", "platform", platform, "error", err)
		}
	}
	return nil
}

func (g *Gateway) Addr() string {
	g.listenerMu.Lock()
	listener := g.listener
	g.listenerMu.Unlock()
	if listener != nil {
		return listener.Addr().String()
	}
	return g.server.Addr
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

func (g *Gateway) GetAdapter(platform string) (cobot.PlatformAdapter, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	a, ok := g.adapters[platform]
	return a, ok
}

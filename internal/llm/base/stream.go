package base

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cobot-agent/cobot/internal/debuglog"
)

// MaxScannerBuffer is the maximum size the scanner buffer can grow to (256KB).
const MaxScannerBuffer = 256 * 1024

// DefaultSSEIdleTimeout is the maximum time to wait for the next SSE event
// before aborting the stream. Thinking models may take a long time, so this
// is generous. It only fires when no data arrives at all.
const DefaultSSEIdleTimeout = 5 * time.Minute

// PendingToolCall tracks incremental assembly of a tool call across stream events.
type PendingToolCall struct {
	ID   string
	Name string
	Args strings.Builder
}

// SSEScanner wraps bufio.Scanner to parse Server-Sent Events (SSE) streams.
// It handles "data: " prefixed lines, skips empty lines, and detects "[DONE]".
//
// SSEScanner is context-aware: when the provided context is cancelled, or when
// no data arrives within the idle timeout, the underlying reader is closed to
// unblock any pending Scan() call.
type SSEScanner struct {
	scanner     *bufio.Scanner
	done        bool
	mu          sync.Mutex
	err         error
	body        io.Closer
	cancelWatch context.CancelFunc
	activity    chan struct{}
	closeOnce   sync.Once
	ctx         context.Context
	provider    string
}

// NewSSEScannerWithContext creates a context-aware SSEScanner.
//
// The scanner starts a background watchdog that closes the body when:
//   - ctx is cancelled (user abort, timeout)
//   - no SSE data arrives within idleTimeout (provider stall)
//
// Closing the body causes scanner.Scan() to return an error, which
// unblocks the Next() call and propagates through the provider stream.
//
// Callers MUST call Close() when done (typically via defer) to stop
// the watchdog and release resources.
func NewSSEScannerWithContext(ctx context.Context, body io.ReadCloser, idleTimeout time.Duration, provider ...string) *SSEScanner {
	if idleTimeout <= 0 {
		idleTimeout = DefaultSSEIdleTimeout
	}

	watchCtx, cancelWatch := context.WithCancel(ctx)
	activity := make(chan struct{}, 1)

	var prov string
	if len(provider) > 0 {
		prov = provider[0]
	}

	s := &SSEScanner{
		body:        body,
		cancelWatch: cancelWatch,
		activity:    activity,
		ctx:         ctx,
		provider:    prov,
	}
	s.scanner = bufio.NewScanner(body)
	s.scanner.Buffer(make([]byte, 4096), MaxScannerBuffer)

	// Watchdog goroutine: closes body on ctx cancel or idle timeout.
	go func() {
		idle := time.NewTimer(idleTimeout)
		defer idle.Stop()
		for {
			select {
			case <-watchCtx.Done():
				// Context cancelled (user abort or parent timeout).
				s.closeBody()
				return
			case <-idle.C:
				// No data received within idle timeout — provider stalled.
				s.mu.Lock()
				s.err = fmt.Errorf("SSE idle timeout (%s): no data received", idleTimeout)
				s.mu.Unlock()
				s.closeBody()
				return
			case <-activity:
				// Data received — reset idle timer.
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleTimeout)
			}
		}
	}()

	return s
}

// closeBody closes the underlying body exactly once.
func (s *SSEScanner) closeBody() {
	if s.body == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.body.Close()
	})
}

// Close stops the watchdog goroutine and closes the body.
// Safe to call multiple times.
func (s *SSEScanner) Close() {
	if s.cancelWatch != nil {
		s.cancelWatch()
	}
	s.closeBody()
}

// Next advances to the next SSE data event. It returns:
//   - eventType: currently always "" (reserved for future use)
//   - data: the parsed data payload (without the "data: " prefix), or nil for "[DONE]"
//   - err: any scanning error encountered
//
// When the stream sends "data: [DONE]", data will be nil with no error.
// Callers should stop iterating when data is nil and err is nil.
func (s *SSEScanner) Next() (eventType string, data []byte, err error) {
	for s.scanner.Scan() {
		// Pulse watchdog — data is arriving.
		if s.activity != nil {
			select {
			case s.activity <- struct{}{}:
			default:
			}
		}

		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			s.done = true
			return "", nil, nil
		}
		debuglog.LogSSE(s.ctx, s.provider, []byte(payload))
		return "", []byte(payload), nil
	}

	// If the watchdog set an error (idle timeout), prefer that.
	s.mu.Lock()
	watchdogErr := s.err
	s.mu.Unlock()
	if watchdogErr != nil {
		return "", nil, watchdogErr
	}

	if err := s.scanner.Err(); err != nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		return "", nil, err
	}
	// Stream ended without [DONE]
	return "", nil, io.EOF
}

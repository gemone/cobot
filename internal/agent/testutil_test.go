package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// testRegistry is a minimal ToolRegistry for unit tests inside package agent,
// avoiding an import of internal/tools.
type testRegistry struct {
	mu    sync.RWMutex
	tools map[string]cobot.Tool
}

func newTestRegistry() *testRegistry {
	return &testRegistry{tools: make(map[string]cobot.Tool)}
}

func (r *testRegistry) Register(t cobot.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *testRegistry) Get(name string) (cobot.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return t, nil
}

func (r *testRegistry) ToolDefs() []cobot.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]cobot.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, cobot.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func (r *testRegistry) Execute(ctx context.Context, call cobot.ToolCall) (*cobot.ToolResult, error) {
	r.mu.RLock()
	t, ok := r.tools[call.Name]
	r.mu.RUnlock()
	if !ok {
		return &cobot.ToolResult{CallID: call.ID, Error: fmt.Sprintf("tool %q not found", call.Name)}, nil
	}
	output, err := t.Execute(ctx, call.Arguments)
	if err != nil {
		return &cobot.ToolResult{CallID: call.ID, Error: err.Error()}, nil
	}
	return &cobot.ToolResult{CallID: call.ID, Output: output}, nil
}

func (r *testRegistry) ExecuteParallel(ctx context.Context, calls []cobot.ToolCall) []*cobot.ToolResult {
	results := make([]*cobot.ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c cobot.ToolCall) {
			defer wg.Done()
			result, _ := r.Execute(ctx, c)
			results[idx] = result
		}(i, call)
	}
	wg.Wait()
	return results
}

func (r *testRegistry) Clone() cobot.ToolRegistry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cloned := newTestRegistry()
	for name, t := range r.tools {
		cloned.tools[name] = t
	}
	return cloned
}

func (r *testRegistry) IsStreamingTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return false
	}
	_, streaming := t.(cobot.StreamingTool)
	return streaming
}

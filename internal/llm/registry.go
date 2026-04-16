package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cobot-agent/cobot/internal/llm/anthropic"
	"github.com/cobot-agent/cobot/internal/llm/openai"
	cobot "github.com/cobot-agent/cobot/pkg"
)

type ProviderFactory func(apiKey, baseURL string) cobot.Provider

type Registry struct {
	mu        sync.RWMutex
	providers map[string]cobot.Provider
	factories map[string]ProviderFactory
	config    *cobot.Config
}

func NewRegistry(cfg *cobot.Config) *Registry {
	r := &Registry{
		providers: make(map[string]cobot.Provider),
		factories: make(map[string]ProviderFactory),
		config:    cfg,
	}
	r.factories[anthropic.ProviderName] = func(apiKey, baseURL string) cobot.Provider {
		return anthropic.NewProvider(apiKey, baseURL)
	}
	r.factories[openai.ProviderName] = func(apiKey, baseURL string) cobot.Provider {
		return openai.NewProvider(apiKey, baseURL)
	}
	return r
}

func (r *Registry) RegisterFactory(name string, f ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = f
}

func (r *Registry) Register(name string, p cobot.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
}

func (r *Registry) Get(name string) (cobot.Provider, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if ok {
		return p, nil
	}

	// Lazy-init: create provider on first access.
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if p, ok := r.providers[name]; ok {
		return p, nil
	}

	if r.config == nil {
		return nil, fmt.Errorf("provider %q not found", name)
	}

	p, err := r.createProvider(name)
	if err != nil {
		return nil, err
	}
	r.providers[name] = p
	return p, nil
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// ProviderForModel parses "provider:model" notation and returns the provider
// and model name. If the provider is not yet initialized, it is created lazily.
func (r *Registry) ProviderForModel(modelSpec string) (cobot.Provider, string, error) {
	providerName, modelName := parseModelSpec(modelSpec)
	p, err := r.Get(providerName)
	if err != nil {
		return nil, "", err
	}
	return p, modelName, nil
}

func (r *Registry) ValidateModel(ctx context.Context, modelSpec string) error {
	p, modelName, err := r.ProviderForModel(modelSpec)
	if err != nil {
		return err
	}
	if v, ok := p.(cobot.ModelValidator); ok {
		return v.ValidateModel(ctx, modelName)
	}
	return nil
}

func (r *Registry) createProvider(name string) (cobot.Provider, error) {
	apiKey := r.config.APIKeys[name]
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key not configured", name)
	}

	baseURL := ""
	if r.config.Providers != nil {
		if pc, ok := r.config.Providers[name]; ok {
			baseURL = pc.BaseURL
		}
	}

	factory, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return factory(apiKey, baseURL), nil
}

func parseModelSpec(model string) (providerName, modelName string) {
	parts := strings.SplitN(model, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return openai.ProviderName, model
}

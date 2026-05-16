package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Engine struct {
	mu               sync.RWMutex
	providers        map[string]Provider
	order            []string // registration order
	strategy         Strategy
	metrics          *metricsStore
	breakers         map[string]*circuitBreaker
	localFactories   map[string]func(ProviderConfig) (Provider, error)
}

func NewEngine(strategy Strategy) *Engine {
	return &Engine{
		providers:      make(map[string]Provider),
		strategy:       strategy,
		metrics:        newMetricsStore(),
		breakers:       make(map[string]*circuitBreaker),
		localFactories: make(map[string]func(ProviderConfig) (Provider, error)),
	}
}

// RegisterFactory registers a provider factory local to this engine, overriding
// the global registry for the given name. Useful for injecting mock providers in tests.
func (e *Engine) RegisterFactory(name string, factory func(ProviderConfig) (Provider, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.localFactories[name] = factory
}

// CreateProvider creates a provider using this engine's local factory overrides first,
// falling back to the global registry.
func (e *Engine) CreateProvider(cfg ProviderConfig) (Provider, error) {
	e.mu.RLock()
	factory, ok := e.localFactories[cfg.Name]
	e.mu.RUnlock()
	if ok {
		return factory(cfg)
	}
	return CreateProvider(cfg)
}

func (e *Engine) Register(p Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.providers[p.Name()]; !exists {
		e.order = append(e.order, p.Name())
		e.breakers[p.Name()] = &circuitBreaker{}
	}
	e.providers[p.Name()] = p
}

func (e *Engine) SetStrategy(s Strategy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.strategy = s
}

func (e *Engine) GetProvider(name string) (Provider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.providers[name]
	return p, ok
}

func (e *Engine) Providers() []Provider {
	e.mu.RLock()
	defer e.mu.RUnlock()
	providers := make([]Provider, 0, len(e.order))
	for _, name := range e.order {
		providers = append(providers, e.providers[name])
	}
	return providers
}

func (e *Engine) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	ordered := e.selectProviders(req)

	var errs []error
	for _, p := range ordered {
		cb := e.breakerFor(p.Name())
		if !cb.allow() {
			errs = append(errs, &ProviderError{Provider: p.Name(), Message: "circuit open"})
			continue
		}
		var resp *ChatResponse
		err := retryCall(ctx, func() error {
			var callErr error
			resp, callErr = e.callProvider(ctx, p, req)
			if callErr != nil {
				e.metrics.recordError(p.Name())
				cb.recordFailure()
			}
			return callErr
		})
		if err == nil {
			cb.recordSuccess()
			return resp, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("core: no providers available")
	}
	return nil, &MultiError{Errors: errs}
}

func (e *Engine) ChatWithProvider(ctx context.Context, req *ChatRequest, name string) (*ChatResponse, error) {
	e.mu.RLock()
	p, ok := e.providers[name]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("core: provider %q not found", name)
	}
	cb := e.breakerFor(name)
	if !cb.allow() {
		return nil, &ProviderError{Provider: name, Message: "circuit open"}
	}
	var resp *ChatResponse
	err := retryCall(ctx, func() error {
		var callErr error
		resp, callErr = e.callProvider(ctx, p, req)
		if callErr != nil {
			e.metrics.recordError(name)
			cb.recordFailure()
		}
		return callErr
	})
	if err != nil {
		return nil, err
	}
	cb.recordSuccess()
	return resp, nil
}

func (e *Engine) ChatWithFallback(ctx context.Context, req *ChatRequest, names []string) (*ChatResponse, error) {
	var errs []error
	for _, name := range names {
		e.mu.RLock()
		p, ok := e.providers[name]
		e.mu.RUnlock()
		if !ok {
			errs = append(errs, fmt.Errorf("core: provider %q not found", name))
			continue
		}
		cb := e.breakerFor(name)
		if !cb.allow() {
			errs = append(errs, &ProviderError{Provider: name, Message: "circuit open"})
			continue
		}
		var resp *ChatResponse
		err := retryCall(ctx, func() error {
			var callErr error
			resp, callErr = e.callProvider(ctx, p, req)
			if callErr != nil {
				e.metrics.recordError(name)
				cb.recordFailure()
			}
			return callErr
		})
		if err == nil {
			cb.recordSuccess()
			return resp, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("core: all fallback providers failed")
	}
	return nil, &MultiError{Errors: errs}
}

func (e *Engine) Snapshot() MetricsSnapshot {
	return e.metrics.snapshot()
}

func (e *Engine) selectProviders(req *ChatRequest) []Provider {
	e.mu.RLock()
	defer e.mu.RUnlock()
	all := make([]Provider, 0, len(e.order))
	for _, name := range e.order {
		all = append(all, e.providers[name])
	}
	if e.strategy != nil {
		snap := e.metrics.snapshot()
		return e.strategy.Select(all, req, &snap)
	}
	return all
}

func (e *Engine) callProvider(ctx context.Context, p Provider, req *ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Provider = p.Name()
	resp.Latency = time.Since(start)
	e.metrics.recordSuccess(p.Name(), resp.Latency)
	return resp, nil
}

func (e *Engine) breakerFor(name string) *circuitBreaker {
	e.mu.RLock()
	cb := e.breakers[name]
	e.mu.RUnlock()
	return cb
}

func (e *Engine) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	ordered := e.selectProviders(req)
	var errs []error
	for _, p := range ordered {
		cb := e.breakerFor(p.Name())
		if !cb.allow() {
			errs = append(errs, &ProviderError{Provider: p.Name(), Message: "circuit open"})
			continue
		}
		ch, err := p.ChatStream(ctx, req)
		if err != nil {
			e.metrics.recordError(p.Name())
			cb.recordFailure()
			errs = append(errs, err)
			continue
		}
		return ch, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("core: no providers available")
	}
	return nil, &MultiError{Errors: errs}
}

func (e *Engine) ChatStreamWithProvider(ctx context.Context, req *ChatRequest, name string) (<-chan StreamChunk, error) {
	e.mu.RLock()
	p, ok := e.providers[name]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("core: provider %q not found", name)
	}
	return p.ChatStream(ctx, req)
}

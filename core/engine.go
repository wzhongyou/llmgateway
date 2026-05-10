package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Engine struct {
	mu        sync.RWMutex
	providers map[string]Provider
	order     []string // registration order
	strategy  Strategy
	metrics   *metricsStore
}

func NewEngine(strategy Strategy) *Engine {
	return &Engine{
		providers: make(map[string]Provider),
		strategy:  strategy,
		metrics:   newMetricsStore(),
	}
}

func (e *Engine) Register(p Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.providers[p.Name()]; !exists {
		e.order = append(e.order, p.Name())
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

	var lastErr error
	for _, p := range ordered {
		resp, err := e.callProvider(ctx, p, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		e.metrics.recordError(p.Name())
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("core: no providers available")
}

func (e *Engine) ChatWithProvider(ctx context.Context, req *ChatRequest, name string) (*ChatResponse, error) {
	e.mu.RLock()
	p, ok := e.providers[name]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("core: provider %q not found", name)
	}
	return e.callProvider(ctx, p, req)
}

func (e *Engine) ChatWithFallback(ctx context.Context, req *ChatRequest, names []string) (*ChatResponse, error) {
	var lastErr error
	for _, name := range names {
		e.mu.RLock()
		p, ok := e.providers[name]
		e.mu.RUnlock()
		if !ok {
			lastErr = fmt.Errorf("core: provider %q not found", name)
			continue
		}
		resp, err := e.callProvider(ctx, p, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		e.metrics.recordError(p.Name())
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("core: all fallback providers failed")
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

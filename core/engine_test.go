package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzhongyou/llmgate/core"
)

// mockProvider is a controllable Provider for unit tests.
type mockProvider struct {
	name     string
	response *core.ChatResponse
	err      error
	calls    int
}

func (m *mockProvider) Name() string     { return m.name }
func (m *mockProvider) Models() []string { return nil }

func (m *mockProvider) Chat(_ context.Context, _ *core.ChatRequest) (*core.ChatResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	resp := *m.response
	return &resp, nil
}

func (m *mockProvider) ChatStream(_ context.Context, _ *core.ChatRequest) (<-chan core.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan core.StreamChunk, 1)
	ch <- core.StreamChunk{Content: m.response.Content}
	close(ch)
	return ch, nil
}

// callCounter is a mock whose Chat is driven by a function.
type callCounter struct {
	name string
	fn   func() (*core.ChatResponse, error)
}

func (c *callCounter) Name() string     { return c.name }
func (c *callCounter) Models() []string { return nil }
func (c *callCounter) Chat(_ context.Context, _ *core.ChatRequest) (*core.ChatResponse, error) {
	return c.fn()
}
func (c *callCounter) ChatStream(_ context.Context, _ *core.ChatRequest) (<-chan core.StreamChunk, error) {
	resp, err := c.fn()
	if err != nil {
		return nil, err
	}
	ch := make(chan core.StreamChunk, 1)
	ch <- core.StreamChunk{Content: resp.Content}
	close(ch)
	return ch, nil
}

func newEngine(providers ...core.Provider) *core.Engine {
	e := core.NewEngine(nil)
	for _, p := range providers {
		e.Register(p)
	}
	return e
}

func TestEngine_Chat_Success(t *testing.T) {
	p := &mockProvider{name: "a", response: &core.ChatResponse{Content: "hello"}}
	e := newEngine(p)

	resp, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("expected 'hello', got %q", resp.Content)
	}
	if resp.Provider != "a" {
		t.Errorf("expected provider 'a', got %q", resp.Provider)
	}
}

func TestEngine_Chat_FallsBackOnError(t *testing.T) {
	bad := &mockProvider{name: "bad", err: &core.ProviderError{Provider: "bad", Message: "fail"}}
	good := &mockProvider{name: "good", response: &core.ChatResponse{Content: "ok"}}
	e := newEngine(bad, good)

	resp, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "good" {
		t.Errorf("expected fallback to 'good', got %q", resp.Provider)
	}
}

func TestEngine_Chat_AggregatesErrors(t *testing.T) {
	e := newEngine(
		&mockProvider{name: "a", err: &core.ProviderError{Provider: "a", Message: "fail a"}},
		&mockProvider{name: "b", err: &core.ProviderError{Provider: "b", Message: "fail b"}},
	)

	_, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	var multi *core.MultiError
	if !errors.As(err, &multi) {
		t.Fatalf("expected MultiError, got %T: %v", err, err)
	}
	if len(multi.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(multi.Errors))
	}
}

func TestEngine_Chat_NoProviders(t *testing.T) {
	e := core.NewEngine(nil)
	_, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err == nil {
		t.Fatal("expected error with no providers")
	}
}

func TestEngine_ChatWithProvider_NotFound(t *testing.T) {
	e := core.NewEngine(nil)
	_, err := e.ChatWithProvider(context.Background(), &core.ChatRequest{}, "missing")
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestEngine_ChatWithFallback(t *testing.T) {
	good := &mockProvider{name: "good", response: &core.ChatResponse{Content: "ok"}}
	e := newEngine(good)

	resp, err := e.ChatWithFallback(context.Background(), &core.ChatRequest{}, []string{"missing", "good"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "good" {
		t.Errorf("expected 'good', got %q", resp.Provider)
	}
}

func TestEngine_CircuitBreaker_OpensAfterFailures(t *testing.T) {
	p := &mockProvider{
		name: "flaky",
		err:  &core.ProviderError{Provider: "flaky", Message: "fail", Retryable: false},
	}
	e := newEngine(p)

	// Drive the breaker open (threshold = 5 consecutive failures)
	for range 5 {
		e.ChatWithProvider(context.Background(), &core.ChatRequest{}, "flaky") //nolint:errcheck
	}
	callsBefore := p.calls

	// Next call should be rejected by open circuit without calling the provider
	e.ChatWithProvider(context.Background(), &core.ChatRequest{}, "flaky") //nolint:errcheck
	if p.calls != callsBefore {
		t.Errorf("expected circuit to block call, but provider was called (calls before=%d, after=%d)", callsBefore, p.calls)
	}
}

func TestEngine_Retry_OnRetryableError(t *testing.T) {
	attempt := 0
	retryableErr := &core.ProviderError{Provider: "retry", Message: "temp", Retryable: true}
	p := &callCounter{
		name: "retry",
		fn: func() (*core.ChatResponse, error) {
			attempt++
			if attempt < 2 {
				return nil, retryableErr
			}
			return &core.ChatResponse{Content: "ok"}, nil
		},
	}
	e := newEngine(p)

	resp, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if attempt < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempt)
	}
}

func TestEngine_Metrics_RecordsSuccess(t *testing.T) {
	p := &mockProvider{name: "m", response: &core.ChatResponse{Content: "x"}}
	e := newEngine(p)

	_, err := e.Chat(context.Background(), &core.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}

	snap := e.Snapshot()
	stat, ok := snap.Providers["m"]
	if !ok {
		t.Fatal("expected metrics for provider 'm'")
	}
	if stat.ErrorRate != 0 {
		t.Errorf("expected zero error rate, got %.2f", stat.ErrorRate)
	}
	if !stat.Available {
		t.Error("expected provider to be available")
	}
}

func TestEngine_Metrics_RecordsError(t *testing.T) {
	p := &mockProvider{name: "m", err: &core.ProviderError{Provider: "m", Message: "fail"}}
	e := newEngine(p)

	e.Chat(context.Background(), &core.ChatRequest{}) //nolint:errcheck

	snap := e.Snapshot()
	stat := snap.Providers["m"]
	if stat.ErrorRate != 1.0 {
		t.Errorf("expected error rate 1.0, got %.2f", stat.ErrorRate)
	}
}

func TestEngine_RegisterFactory_LocalOverride(t *testing.T) {
	e := core.NewEngine(nil)
	e.RegisterFactory("mock", func(cfg core.ProviderConfig) (core.Provider, error) {
		return &mockProvider{name: "mock", response: &core.ChatResponse{Content: "injected"}}, nil
	})
	p, err := e.CreateProvider(core.ProviderConfig{Name: "mock", Key: "x"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	resp, _ := p.Chat(context.Background(), &core.ChatRequest{})
	if resp.Content != "injected" {
		t.Errorf("expected 'injected', got %q", resp.Content)
	}
}

func TestTimeBasedStrategy_InjectNow(t *testing.T) {
	day := &mockProvider{name: "day", response: &core.ChatResponse{Content: "day"}}
	night := &mockProvider{name: "night", response: &core.ChatResponse{Content: "night"}}

	s := core.NewTimeBasedStrategy("day", "night")

	// 2pm → day
	s.SetNowFn(func() time.Time { return time.Date(2024, 1, 1, 14, 0, 0, 0, time.UTC) })
	ordered := s.Select([]core.Provider{day, night}, &core.ChatRequest{}, &core.MetricsSnapshot{})
	if ordered[0].Name() != "day" {
		t.Errorf("expected 'day' first at 2pm, got %q", ordered[0].Name())
	}

	// 2am → night
	s.SetNowFn(func() time.Time { return time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC) })
	ordered = s.Select([]core.Provider{day, night}, &core.ChatRequest{}, &core.MetricsSnapshot{})
	if ordered[0].Name() != "night" {
		t.Errorf("expected 'night' first at 2am, got %q", ordered[0].Name())
	}
}

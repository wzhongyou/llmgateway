// Package console provides a developer-focused web console for the llmgate proxy.
// It includes channel management, a chat playground, mock response stubs, and
// a ring buffer of recent requests — all served from a single binary.
package console

import (
	"context"
	"crypto/rand"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/wzhongyou/llmgate/core"
)

//go:embed static/*
var staticFiles embed.FS

// Config holds the external dependencies for creating a Console.
type Config struct {
	Engine          *core.Engine
	ConfigPath      string
	AdminToken      string
	RawProviderKeys map[string]string
	SaveConfig      func() error // called by handleConfigSave
}

// Console is the central coordinator for the developer console.
type Console struct {
	mu              sync.RWMutex
	engine          *core.Engine
	configPath      string
	adminToken      string
	rawProviderKeys map[string]string
	recentReqs      *ringBuffer
	mockStore       *MockStore
	saveConfig      func() error
}

// New creates a Console, registers the mock provider on the engine,
// and initialises the request ring buffer.
func New(cfg Config) *Console {
	store := &MockStore{}
	mp := &mockProvider{store: store}
	cfg.Engine.Register(mp)

	c := &Console{
		engine:          cfg.Engine,
		configPath:      cfg.ConfigPath,
		adminToken:      cfg.AdminToken,
		rawProviderKeys: cfg.RawProviderKeys,
		recentReqs:      newRingBuffer(200),
		mockStore:       store,
		saveConfig:      cfg.SaveConfig,
	}
	return c
}

// RecordRequest pushes a request record into the ring buffer.
func (c *Console) RecordRequest(e RecentEntry) {
	c.recentReqs.Push(e)
}

// MockStore is a thread-safe container for mock rules.
type MockStore struct {
	mu    sync.RWMutex
	rules []MockRule
}

// Rules returns a copy of all rules.
func (s *MockStore) Rules() []MockRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MockRule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Set replaces the entire rule set atomically.
func (s *MockStore) Set(rules []MockRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = rules
}

// MockRule defines a single mock stub rule.
type MockRule struct {
	ID         string `json:"id"`
	Enabled    bool   `json:"enabled"`
	MatchModel string `json:"match_model"`
	Priority   int    `json:"priority"`
	Action     string `json:"action"`      // "response", "error", "timeout", "empty"
	StatusCode int    `json:"status_code"` // used by "error" action
	Content    string `json:"content"`     // used by "response" action
	ErrorMsg   string `json:"error_msg"`   // used by "error" action
	DelayMs    int    `json:"delay_ms"`    // used by "timeout" action
}

// mockProvider implements core.Provider and returns canned responses.
type mockProvider struct {
	store *MockStore
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Models() []string {
	rules := m.store.Rules()
	seen := make(map[string]bool)
	var models []string
	for _, r := range rules {
		if r.Enabled && !seen[r.MatchModel] {
			seen[r.MatchModel] = true
			models = append(models, r.MatchModel)
		}
	}
	return models
}

func (m *mockProvider) findRule(req *core.ChatRequest) (*MockRule, bool) {
	rules := m.store.Rules()
	var best *MockRule
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		if r.MatchModel != "" && req.Model != r.MatchModel {
			continue
		}
		if best == nil || r.Priority > best.Priority {
			best = r
		}
	}
	return best, best != nil
}

func (m *mockProvider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	rule, ok := m.findRule(req)
	if !ok {
		return &core.ChatResponse{
			Model:    req.Model,
			Content:  "mock: no matching rule",
			Provider: "mock",
		}, nil
	}
	return m.apply(ctx, rule, req.Model)
}

func (m *mockProvider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	rule, ok := m.findRule(req)
	if !ok {
		ch := make(chan core.StreamChunk, 1)
		ch <- core.StreamChunk{Content: "mock: no matching rule", Model: req.Model}
		close(ch)
		return ch, nil
	}
	return m.applyStream(ctx, rule, req.Model)
}

func (m *mockProvider) apply(ctx context.Context, rule *MockRule, model string) (*core.ChatResponse, error) {
	switch rule.Action {
	case "error":
		return nil, &core.ProviderError{
			Provider:   "mock",
			StatusCode: rule.StatusCode,
			Message:    rule.ErrorMsg,
		}
	case "timeout":
		select {
		case <-time.After(time.Duration(rule.DelayMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &core.ChatResponse{
			Model:    model,
			Content:  rule.Content,
			Provider: "mock",
		}, nil
	case "empty":
		return &core.ChatResponse{
			Model:    model,
			Content:  "",
			Provider: "mock",
		}, nil
	default: // "response"
		return &core.ChatResponse{
			Model:    model,
			Content:  rule.Content,
			Provider: "mock",
		}, nil
	}
}

func (m *mockProvider) applyStream(ctx context.Context, rule *MockRule, model string) (<-chan core.StreamChunk, error) {
	ch := make(chan core.StreamChunk, 1)
	go func() {
		defer close(ch)
		switch rule.Action {
		case "error":
			ch <- core.StreamChunk{
				Error: &core.ProviderError{
					Provider:   "mock",
					StatusCode: rule.StatusCode,
					Message:    rule.ErrorMsg,
				},
				Model: model,
			}
		case "timeout":
			select {
			case <-time.After(time.Duration(rule.DelayMs) * time.Millisecond):
			case <-ctx.Done():
				return
			}
			if rule.Content != "" {
				ch <- core.StreamChunk{Content: rule.Content, Model: model}
			}
		case "empty":
			// send nothing
		default: // "response"
			if rule.Content != "" {
				ch <- core.StreamChunk{Content: rule.Content, Model: model}
			}
		}
	}()
	return ch, nil
}

// --- ring buffer ---

type RecentEntry struct {
	ID           string              `json:"id"`
	Time         time.Time           `json:"time"`
	Provider     string              `json:"provider"`
	Model        string              `json:"model"`
	Status       int                 `json:"status"`
	LatencyMs    float64             `json:"latency_ms"`
	InputTokens  int                 `json:"input_tokens"`
	OutputTokens int                 `json:"output_tokens"`
	Request      *core.ChatRequest   `json:"request"`
	Response     *core.ChatResponse  `json:"response"`
	Error        string              `json:"error,omitempty"`
	Stream       bool                `json:"stream"`
}

type ringBuffer struct {
	mu    sync.Mutex
	buf   []RecentEntry
	head  int
	count int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]RecentEntry, size)}
}

func (rb *ringBuffer) Push(e RecentEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	e.ID = newID()
	rb.buf[rb.head] = e
	rb.head = (rb.head + 1) % len(rb.buf)
	if rb.count < len(rb.buf) {
		rb.count++
	}
}

// List returns entries from oldest to newest, with request/response stripped for the summary view.
func (rb *ringBuffer) List(limit int) []RecentEntry {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	entries := rb.snapshot()
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}
	for i := range entries {
		entries[i].Request = nil
		entries[i].Response = nil
	}
	return entries
}

// Get returns a single entry by ID with full request/response.
func (rb *ringBuffer) Get(id string) (RecentEntry, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for _, e := range rb.snapshot() {
		if e.ID == id {
			return e, true
		}
	}
	return RecentEntry{}, false
}

func (rb *ringBuffer) snapshot() []RecentEntry {
	out := make([]RecentEntry, 0, rb.count)
	if rb.count == 0 {
		return out
	}
	start := rb.head - rb.count
	if start < 0 {
		start += len(rb.buf)
		// Two segments: [start .. end] + [0 .. head-1]
		out = append(out, rb.buf[start:]...)
		out = append(out, rb.buf[:rb.head]...)
	} else {
		out = append(out, rb.buf[start:rb.head]...)
	}
	return out
}

func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("%x", b[:])
}

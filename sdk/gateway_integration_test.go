package sdk_test

import (
	"context"
	"testing"

	"github.com/wzhongyou/llmgate"

	_ "github.com/wzhongyou/llmgate/core/providers/deepseek"
	_ "github.com/wzhongyou/llmgate/core/providers/glm"
	_ "github.com/wzhongyou/llmgate/core/providers/minimax"
)

// requireProvider skips the test if the named provider is not configured.
// Configuration comes from llmgate.toml or the corresponding env var.
func requireProvider(t *testing.T, gw *llmgate.Gateway, name string) {
	t.Helper()
	if _, ok := gw.Engine().GetProvider(name); !ok {
		t.Skipf("%s not configured (set %s_KEY or add to llmgate.toml)", name, name)
	}
}

func newGateway(t *testing.T) *llmgate.Gateway {
	t.Helper()
	return llmgate.New()
}

func TestSDK_DeepSeek_Chat(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "deepseek")

	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{
			{Role: "user", Content: "你好，请用一句话介绍你自己。"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty content")
	}
	if resp.Provider != "deepseek" {
		t.Errorf("expected provider=deepseek, got %q", resp.Provider)
	}
	if resp.Usage.TotalTokens <= 0 {
		t.Errorf("expected TotalTokens > 0, got %d", resp.Usage.TotalTokens)
	}
	if resp.Latency <= 0 {
		t.Error("expected Latency > 0")
	}
	t.Logf("Model: %s, Tokens: %d (in=%d out=%d reasoning=%d), Latency: %v",
		resp.Model, resp.Usage.TotalTokens,
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.ReasoningTokens,
		resp.Latency)
}

func TestSDK_DeepSeek_WithProvider(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "deepseek")

	resp, err := gw.With("deepseek").Chat(context.Background(), &llmgate.ChatRequest{
		Messages:  []llmgate.Message{{Role: "user", Content: "1+1=?"}},
		MaxTokens: intPtr(50),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	t.Logf("[With] Provider: %s, Content: %s", resp.Provider, resp.Content)
}

func TestSDK_DeepSeek_SystemPrompt(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "deepseek")

	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		System:   "你是一个数学家，回答必须简洁，不超过20个字。",
		Messages: []llmgate.Message{{Role: "user", Content: "1+1=?"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len([]rune(resp.Content)) > 50 {
		t.Errorf("expected short response, got %d chars: %s", len([]rune(resp.Content)), resp.Content)
	}
	t.Logf("Content: %s", resp.Content)
}

func TestSDK_MetricsSnapshot(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "deepseek")

	if _, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{{Role: "user", Content: "Hi"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	stat, ok := gw.Snapshot().Providers["deepseek"]
	if !ok {
		t.Fatal("expected deepseek in metrics snapshot")
	}
	if stat.AvgLatencyMs <= 0 {
		t.Errorf("expected AvgLatencyMs > 0, got %.2f", stat.AvgLatencyMs)
	}
	t.Logf("Metrics: ErrorRate=%.2f, AvgLatencyMs=%.2f",
		stat.ErrorRate, stat.AvgLatencyMs)
}

func TestSDK_GLM_Chat(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "glm")

	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{
			{Role: "user", Content: "你好，请用一句话介绍你自己。"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty content")
	}
	if resp.Provider != "glm" {
		t.Errorf("expected provider=glm, got %q", resp.Provider)
	}
	if resp.Usage.TotalTokens <= 0 {
		t.Errorf("expected TotalTokens > 0, got %d", resp.Usage.TotalTokens)
	}
	t.Logf("Model: %s, Tokens: %d (in=%d out=%d reasoning=%d), Latency: %v",
		resp.Model, resp.Usage.TotalTokens,
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.ReasoningTokens,
		resp.Latency)
}

func TestSDK_MiniMax_Chat(t *testing.T) {
	gw := newGateway(t)
	requireProvider(t, gw, "minimax")

	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{
			{Role: "user", Content: "你好，请用一句话介绍你自己。"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty content")
	}
	if resp.Provider != "minimax" {
		t.Errorf("expected provider=minimax, got %q", resp.Provider)
	}
	if resp.Usage.TotalTokens <= 0 {
		t.Errorf("expected TotalTokens > 0, got %d", resp.Usage.TotalTokens)
	}
	t.Logf("Model: %s, Tokens: %d (in=%d out=%d reasoning=%d), Latency: %v",
		resp.Model, resp.Usage.TotalTokens,
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.ReasoningTokens,
		resp.Latency)
}

func TestSDK_GLM_Fallback(t *testing.T) {
	gw := newGateway(t)
	_, hasGLM := gw.Engine().GetProvider("glm")
	_, hasDeepSeek := gw.Engine().GetProvider("deepseek")
	if !hasGLM && !hasDeepSeek {
		t.Skip("neither glm nor deepseek configured")
	}

	resp, err := gw.Fallback("glm", "deepseek").Chat(context.Background(), &llmgate.ChatRequest{
		Messages:  []llmgate.Message{{Role: "user", Content: "1+1=?"}},
		MaxTokens: intPtr(20),
	})
	if err != nil {
		t.Fatalf("Fallback Chat: %v", err)
	}
	t.Logf("[Fallback] Provider: %s, Content: %s", resp.Provider, resp.Content)
}

func intPtr(i int) *int { return &i }

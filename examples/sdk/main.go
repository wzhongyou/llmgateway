package main

import (
	"context"
	"fmt"

	"github.com/wzhongyou/llmgateway"

	// Register built-in providers
	_ "github.com/wzhongyou/llmgateway/core/providers/anthropic"
	_ "github.com/wzhongyou/llmgateway/core/providers/deepseek"
	_ "github.com/wzhongyou/llmgateway/core/providers/ernie"
	_ "github.com/wzhongyou/llmgateway/core/providers/gemini"
	_ "github.com/wzhongyou/llmgateway/core/providers/glm"
	_ "github.com/wzhongyou/llmgateway/core/providers/grok"
	_ "github.com/wzhongyou/llmgateway/core/providers/hunyuan"
	_ "github.com/wzhongyou/llmgateway/core/providers/kimi"
	_ "github.com/wzhongyou/llmgateway/core/providers/llama"
	_ "github.com/wzhongyou/llmgateway/core/providers/mimo"
	_ "github.com/wzhongyou/llmgateway/core/providers/minimax"
	_ "github.com/wzhongyou/llmgateway/core/providers/openai"
	_ "github.com/wzhongyou/llmgateway/core/providers/qwen"
	_ "github.com/wzhongyou/llmgateway/core/providers/stepfun"
)

func main() {
	// New() auto-loads from llmgateway.toml or env vars (DEEPSEEK_KEY, etc.)
	gw := llmgateway.New()

	providers := gw.ProviderNames()
	if len(providers) == 0 {
		fmt.Println("No provider configured.")
		fmt.Println("Options:")
		fmt.Println("  1. cp llmgateway.toml.example llmgateway.toml  (then fill in keys)")
		fmt.Println("  2. export GLM_KEY=xxx / MINIMAX_KEY=xxx / DEEPSEEK_KEY=xxx")
		fmt.Println("  3. call gw.Use(\"glm\", \"your-key\") in code")
		return
	}

	fmt.Printf("Loaded providers: %v\n", providers)

	ctx := context.Background()
	req := &llmgateway.ChatRequest{
		Messages: []llmgateway.Message{
			{Role: "user", Content: "Hello, how are you?"},
		},
	}

	// Strategy-based routing: follows [strategy] in config (primary -> fallback)
	resp, err := gw.Chat(ctx, req)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[%s] %s (latency: %v, tokens: %d)\n", resp.Provider, resp.Content, resp.Latency, resp.Usage.TotalTokens)

	// Pin to a specific provider with With()
	if len(providers) > 0 {
		resp2, err := gw.With(providers[0]).Chat(ctx, req)
		if err != nil {
			panic(err)
		}
		fmt.Printf("[%s via With] %s\n", resp2.Provider, resp2.Content)
	}

	// Ad-hoc fallback chain with Fallback()
	if len(providers) >= 2 {
		resp3, err := gw.Fallback(providers[1], providers[0]).Chat(ctx, req)
		if err != nil {
			panic(err)
		}
		fmt.Printf("[%s via Fallback] %s\n", resp3.Provider, resp3.Content)
	}
}

package main

import (
	"context"
	"fmt"

	"github.com/wzhongyou/llmgate"

	_ "github.com/wzhongyou/llmgate/core/providers/anthropic"
	_ "github.com/wzhongyou/llmgate/core/providers/deepseek"
	_ "github.com/wzhongyou/llmgate/core/providers/ernie"
	_ "github.com/wzhongyou/llmgate/core/providers/gemini"
	_ "github.com/wzhongyou/llmgate/core/providers/glm"
	_ "github.com/wzhongyou/llmgate/core/providers/grok"
	_ "github.com/wzhongyou/llmgate/core/providers/hunyuan"
	_ "github.com/wzhongyou/llmgate/core/providers/kimi"
	_ "github.com/wzhongyou/llmgate/core/providers/llama"
	_ "github.com/wzhongyou/llmgate/core/providers/mimo"
	_ "github.com/wzhongyou/llmgate/core/providers/minimax"
	_ "github.com/wzhongyou/llmgate/core/providers/openai"
	_ "github.com/wzhongyou/llmgate/core/providers/qwen"
	_ "github.com/wzhongyou/llmgate/core/providers/stepfun"
)

// go run ./examples/sdk/
func main() {
	gw, err := llmgate.NewFromFile("llmgate.toml")
	if err != nil {
		fmt.Println("load config:", err)
		return
	}

	chat(gw)
	chatStream(gw)
}

func chat(gw *llmgate.Gateway) {
	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{{Role: "user", Content: "你好，一句话介绍你自己"}},
	})
	if err != nil {
		fmt.Println("chat error:", err)
		return
	}
	fmt.Printf("[%s] %s\n", resp.Provider, resp.Content)
}

func chatStream(gw *llmgate.Gateway) {
	ch, err := gw.ChatStream(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{{Role: "user", Content: "数到5，每个数字单独一行"}},
	})
	if err != nil {
		fmt.Println("stream error:", err)
		return
	}
	fmt.Print("[stream] ")
	for chunk := range ch {
		if chunk.Error != nil {
			fmt.Println("\nstream error:", chunk.Error)
			return
		}
		fmt.Print(chunk.Content)
	}
	fmt.Println()
}

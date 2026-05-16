package main

import (
	"context"
	"fmt"

	"github.com/wzhongyou/llmgate"

	_ "github.com/wzhongyou/llmgate/core/providers/anthropic"
	_ "github.com/wzhongyou/llmgate/core/providers/gemini"
	_ "github.com/wzhongyou/llmgate/core/providers/openaicompat"
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
	toolCall(gw)
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

func toolCall(gw *llmgate.Gateway) {
	maxTokens := 256
	weatherTool := llmgate.Tool{
		Type: "function",
		Function: llmgate.ToolFunction{
			Name:        "get_weather",
			Description: "获取指定城市的当前天气",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{
						"type":        "string",
						"description": "城市名称",
					},
				},
				"required": []string{"city"},
			},
		},
	}

	// 第一轮：让模型决定调用哪个工具
	resp, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages:   []llmgate.Message{{Role: "user", Content: "北京今天天气怎么样？"}},
		MaxTokens:  &maxTokens,
		Tools:      []llmgate.Tool{weatherTool},
		ToolChoice: "auto",
	})
	if err != nil {
		fmt.Println("[tool] error:", err)
		return
	}

	if len(resp.ToolCalls) == 0 {
		fmt.Printf("[tool] 模型直接回复（未调用工具）: %s\n", resp.Content)
		return
	}

	tc := resp.ToolCalls[0]
	fmt.Printf("[tool] 第一轮 finish_reason=%s  调用=%s  参数=%s\n",
		resp.FinishReason, tc.Function.Name, tc.Function.Arguments)

	// 第二轮：把工具结果发回去
	resp2, err := gw.Chat(context.Background(), &llmgate.ChatRequest{
		Messages: []llmgate.Message{
			{Role: "user", Content: "北京今天天气怎么样？"},
			{Role: "assistant", ToolCalls: resp.ToolCalls},
			{Role: "tool", ToolCallID: tc.ID, Content: `{"temp":"28°C","condition":"晴，东南风3级"}`},
		},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		fmt.Println("[tool] second turn error:", err)
		return
	}
	fmt.Printf("[tool] 最终回复: %s\n", resp2.Content)
}

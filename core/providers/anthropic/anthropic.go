package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/wzhongyou/llmgate/core"
)

const defaultBaseURL = "https://api.anthropic.com/v1"

func init() {
	core.RegisterProviderEnv("ANTHROPIC_KEY", "anthropic")
	core.RegisterProvider("anthropic", func(cfg core.ProviderConfig) (core.Provider, error) {
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		defaultModel := cfg.DefaultModel
		if defaultModel == "" {
			defaultModel = "claude-sonnet-4-6"
		}
		return &Provider{
			key:          cfg.Key,
			baseURL:      baseURL,
			defaultModel: defaultModel,
		}, nil
	})
}

type Provider struct {
	key          string
	baseURL      string
	defaultModel string
	client       http.Client
}

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) Models() []string {
	return []string{
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}
}

// anthropicMessages converts ChatRequest messages to Anthropic's content-block format.
func anthropicMessages(req *core.ChatRequest) []interface{} {
	var msgs []interface{}
	for _, m := range req.Messages {
		switch {
		case m.Role == "tool":
			// Tool result → Anthropic user message with tool_result block
			msgs = append(msgs, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
				}},
			})
		case len(m.ToolCalls) > 0:
			// Assistant with tool calls → multi-part content blocks
			var parts []map[string]interface{}
			if m.Content != "" {
				parts = append(parts, map[string]interface{}{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				if input == nil {
					input = map[string]interface{}{}
				}
				parts = append(parts, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			msgs = append(msgs, map[string]interface{}{"role": "assistant", "content": parts})
		default:
			msgs = append(msgs, map[string]interface{}{"role": m.Role, "content": m.Content})
		}
	}
	return msgs
}

// anthropicTools converts Tool definitions to Anthropic format (input_schema instead of parameters).
func anthropicTools(tools []core.Tool) []map[string]interface{} {
	result := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		result[i] = map[string]interface{}{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		}
	}
	return result
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	maxTokens := 1024
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   anthropicMessages(req),
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = anthropicTools(req.Tools)
		if req.ToolChoice != nil {
			body["tool_choice"] = req.ToolChoice
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("x-api-key", p.key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &core.ProviderError{Provider: "anthropic", StatusCode: resp.StatusCode, Message: string(respBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}

	var result struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}

	var content string
	var toolCalls []core.ToolCall
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			content += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, core.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: core.FunctionCall{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			})
		}
	}
	if content == "" && len(toolCalls) == 0 {
		return nil, &core.ProviderError{Provider: "anthropic", Message: "empty response"}
	}

	finishReason := result.StopReason
	if finishReason == "tool_use" {
		finishReason = "tool_calls"
	}

	totalTokens := result.Usage.InputTokens + result.Usage.OutputTokens
	return &core.ChatResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Model:        result.Model,
		Usage: core.Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  totalTokens,
		},
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	maxTokens := 1024
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   anthropicMessages(req),
		"stream":     true,
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = anthropicTools(req.Tools)
		if req.ToolChoice != nil {
			body["tool_choice"] = req.ToolChoice
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("x-api-key", p.key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &core.ProviderError{Provider: "anthropic", StatusCode: resp.StatusCode, Message: string(errBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}

	ch := make(chan core.StreamChunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		type toolAcc struct {
			id   string
			name string
			args strings.Builder
		}
		// blockType tracks what type each content block index is ("text" or "tool_use")
		blockType := map[int]string{}
		toolAccs := map[int]*toolAcc{}
		var inputTokens int

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
				// message_start
				Message *struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				// content_block_start
				ContentBlock *struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
				// content_block_delta
				Delta *struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
				// message_delta
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				select {
				case ch <- core.StreamChunk{Error: err}:
				case <-ctx.Done():
				}
				return
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil {
					inputTokens = event.Message.Usage.InputTokens
				}
			case "content_block_start":
				if event.ContentBlock != nil {
					blockType[event.Index] = event.ContentBlock.Type
					if event.ContentBlock.Type == "tool_use" {
						toolAccs[event.Index] = &toolAcc{
							id:   event.ContentBlock.ID,
							name: event.ContentBlock.Name,
						}
					}
				}
			case "content_block_delta":
				if event.Delta == nil {
					continue
				}
				switch event.Delta.Type {
				case "text_delta":
					if event.Delta.Text != "" {
						select {
						case ch <- core.StreamChunk{Content: event.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				case "input_json_delta":
					if acc, ok := toolAccs[event.Index]; ok {
						acc.args.WriteString(event.Delta.PartialJSON)
					}
				}
			case "message_delta":
				if event.Usage != nil {
					outputTokens := event.Usage.OutputTokens
					total := inputTokens + outputTokens
					select {
					case ch <- core.StreamChunk{Usage: &core.Usage{
						InputTokens:  inputTokens,
						OutputTokens: outputTokens,
						TotalTokens:  total,
					}}:
					case <-ctx.Done():
						return
					}
				}
			case "message_stop":
				// Emit accumulated tool calls if any
				if len(toolAccs) > 0 {
					calls := make([]core.ToolCall, 0, len(toolAccs))
					for idx := range blockType {
						if blockType[idx] == "tool_use" {
							acc := toolAccs[idx]
							calls = append(calls, core.ToolCall{
								ID:   acc.id,
								Type: "function",
								Function: core.FunctionCall{
									Name:      acc.name,
									Arguments: acc.args.String(),
								},
							})
						}
					}
					select {
					case ch <- core.StreamChunk{ToolCalls: calls, FinishReason: "tool_calls"}:
					case <-ctx.Done():
					}
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- core.StreamChunk{Error: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch, nil
}

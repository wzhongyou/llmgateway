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

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []msg
	for _, m := range req.Messages {
		messages = append(messages, msg{Role: m.Role, Content: m.Content})
	}

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
		"messages":   messages,
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
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
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}

	var content string
	for _, block := range result.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}
	if content == "" {
		return nil, &core.ProviderError{Provider: "anthropic", Message: "empty content"}
	}

	totalTokens := result.Usage.InputTokens + result.Usage.OutputTokens
	return &core.ChatResponse{
		Content: content,
		Model:   result.Model,
		Usage: core.Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  totalTokens,
		},
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []msg
	for _, m := range req.Messages {
		messages = append(messages, msg{Role: m.Role, Content: m.Content})
	}

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
		"messages":   messages,
		"stream":     true,
	}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "anthropic", Message: err.Error(), Cause: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/messages", bytes.NewReader(payload))
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

		var inputTokens int
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event struct {
				Type    string `json:"type"`
				Message *struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Delta *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
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
			case "content_block_delta":
				if event.Delta != nil && event.Delta.Type == "text_delta" && event.Delta.Text != "" {
					select {
					case ch <- core.StreamChunk{Content: event.Delta.Text}:
					case <-ctx.Done():
						return
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

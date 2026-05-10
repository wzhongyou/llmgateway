package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	httpReq.Header.Set("x-api-key", p.key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	var content string
	for _, block := range result.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}
	if content == "" {
		return nil, fmt.Errorf("anthropic: empty content")
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


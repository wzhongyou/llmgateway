package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wzhongyou/llmgate/core"
)

const defaultBaseURL = "https://api.llama.com/v1"

func init() {
	core.RegisterProvider("llama", func(cfg core.ProviderConfig) (core.Provider, error) {
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		defaultModel := cfg.DefaultModel
		if defaultModel == "" {
			defaultModel = "llama-4-maverick"
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

func (p *Provider) Name() string { return "llama" }

func (p *Provider) Models() []string {
	return []string{
		"llama-4-maverick",
		"llama-4-scout",
		"llama-3.3-70b",
		"llama-3.1-8b",
	}
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []msg

	if req.System != "" {
		messages = append(messages, msg{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		messages = append(messages, msg{Role: m.Role, Content: m.Content})
	}

	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Cause: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Retryable: true, Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &core.ProviderError{Provider: "llama", StatusCode: resp.StatusCode, Message: string(respBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			CompletionDetails *struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Cause: err}
	}

	if len(result.Choices) == 0 {
		return nil, &core.ProviderError{Provider: "llama", Message: "no choices in response"}
	}

	reasoningTokens := 0
	if result.Usage.CompletionDetails != nil {
		reasoningTokens = result.Usage.CompletionDetails.ReasoningTokens
	}

	return &core.ChatResponse{
		Content: result.Choices[0].Message.Content,
		Model:   result.Model,
		Usage: core.Usage{
			InputTokens:     result.Usage.PromptTokens,
			OutputTokens:    result.Usage.CompletionTokens,
			ReasoningTokens: reasoningTokens,
			TotalTokens:     result.Usage.TotalTokens,
		},
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var messages []msg
	if req.System != "" {
		messages = append(messages, msg{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		messages = append(messages, msg{Role: m.Role, Content: m.Content})
	}

	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Cause: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "llama", Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &core.ProviderError{Provider: "llama", StatusCode: resp.StatusCode, Message: string(errBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	return core.OpenAIStream(ctx, resp.Body), nil
}


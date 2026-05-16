package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wzhongyou/llmgate/core"
)

// Provider is a generic OpenAI-compatible LLM provider.
type Provider struct {
	name         string
	key          string
	baseURL      string
	defaultModel string
	models       []string
	client       http.Client
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Models() []string { return p.models }

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	payload, err := json.Marshal(core.OpenAIBody(model, false, req))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &core.ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: string(respBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	return core.OpenAIParseChat(respBody, p.name)
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	payload, err := json.Marshal(core.OpenAIBody(model, true, req))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &core.ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: string(errBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	return core.OpenAIStream(ctx, resp.Body), nil
}

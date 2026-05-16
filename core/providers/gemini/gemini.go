package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wzhongyou/llmgate/core"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

func init() {
	core.RegisterProvider("gemini", func(cfg core.ProviderConfig) (core.Provider, error) {
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		defaultModel := cfg.DefaultModel
		if defaultModel == "" {
			defaultModel = "gemini-3.1-flash"
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

func (p *Provider) Name() string { return "gemini" }

func (p *Provider) Models() []string {
	return []string{"gemini-3.1-pro", "gemini-3.1-flash"}
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	type part struct {
		Text string `json:"text"`
	}
	type geminiContent struct {
		Parts []part `json:"parts"`
		Role  string `json:"role"`
	}
	var contents []geminiContent
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Parts: []part{{Text: m.Content}},
			Role:  role,
		})
	}

	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"contents": contents,
	}
	if req.System != "" {
		body["systemInstruction"] = geminiContent{
			Parts: []part{{Text: req.System}},
		}
	}

	generationConfig := map[string]interface{}{}
	if req.MaxTokens != nil {
		generationConfig["maxOutputTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		generationConfig["temperature"] = *req.Temperature
	}
	if len(generationConfig) > 0 {
		body["generationConfig"] = generationConfig
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Cause: err}
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", p.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("x-goog-api-key", p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Retryable: true, Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &core.ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: string(respBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Cause: err}
	}

	if len(result.Candidates) == 0 {
		return nil, &core.ProviderError{Provider: "gemini", Message: "no candidates in response"}
	}

	var content string
	for _, part := range result.Candidates[0].Content.Parts {
		content += part.Text
	}
	if content == "" {
		return nil, &core.ProviderError{Provider: "gemini", Message: "empty content"}
	}

	return &core.ChatResponse{
		Content: content,
		Model:   model,
		Usage: core.Usage{
			InputTokens:  result.UsageMetadata.PromptTokenCount,
			OutputTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  result.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	type part struct {
		Text string `json:"text"`
	}
	type geminiContent struct {
		Parts []part `json:"parts"`
		Role  string `json:"role"`
	}
	var contents []geminiContent
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Parts: []part{{Text: m.Content}},
			Role:  role,
		})
	}

	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"contents": contents,
	}
	if req.System != "" {
		body["systemInstruction"] = geminiContent{
			Parts: []part{{Text: req.System}},
		}
	}

	generationConfig := map[string]interface{}{}
	if req.MaxTokens != nil {
		generationConfig["maxOutputTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		generationConfig["temperature"] = *req.Temperature
	}
	if len(generationConfig) > 0 {
		body["generationConfig"] = generationConfig
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Cause: err}
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("x-goog-api-key", p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: "gemini", Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &core.ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: string(errBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}

	ch := make(chan core.StreamChunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
				UsageMetadata *struct {
					PromptTokenCount     int `json:"promptTokenCount"`
					CandidatesTokenCount int `json:"candidatesTokenCount"`
					TotalTokenCount      int `json:"totalTokenCount"`
				} `json:"usageMetadata"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				select {
				case ch <- core.StreamChunk{Error: err}:
				case <-ctx.Done():
				}
				return
			}

			if len(event.Candidates) > 0 {
				for _, pt := range event.Candidates[0].Content.Parts {
					if pt.Text != "" {
						select {
						case ch <- core.StreamChunk{Content: pt.Text, Model: model}:
						case <-ctx.Done():
							return
						}
					}
				}
			}

			if event.UsageMetadata != nil {
				select {
				case ch <- core.StreamChunk{
					Model: model,
					Usage: &core.Usage{
						InputTokens:  event.UsageMetadata.PromptTokenCount,
						OutputTokens: event.UsageMetadata.CandidatesTokenCount,
						TotalTokens:  event.UsageMetadata.TotalTokenCount,
					},
				}:
				case <-ctx.Done():
					return
				}
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

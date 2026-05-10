package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wzhongyou/llmgateway/core"
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
		return nil, fmt.Errorf("gemini: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", p.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("gemini: %w", err)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: no candidates in response")
	}

	var content string
	for _, part := range result.Candidates[0].Content.Parts {
		content += part.Text
	}
	if content == "" {
		return nil, fmt.Errorf("gemini: empty content")
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


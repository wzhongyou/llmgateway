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
	core.RegisterProviderEnv("GEMINI_KEY", "gemini")
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

type geminiPart struct {
	Text             string                 `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall    `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse    `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFuncResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role"`
}

// geminiMessages converts ChatRequest messages to Gemini's content format.
func geminiMessages(req *core.ChatRequest) []geminiContent {
	var contents []geminiContent
	for _, m := range req.Messages {
		switch {
		case m.Role == "tool":
			// Tool result → Gemini user message with functionResponse part
			// ToolCallID carries the function name for Gemini round-trips
			var response map[string]interface{}
			if err := json.Unmarshal([]byte(m.Content), &response); err != nil {
				response = map[string]interface{}{"result": m.Content}
			}
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFuncResponse{
						Name:     m.ToolCallID,
						Response: response,
					},
				}},
			})
		case len(m.ToolCalls) > 0:
			// Assistant with tool calls → Gemini model message with functionCall parts
			var parts []geminiPart
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				if args == nil {
					args = map[string]interface{}{}
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			contents = append(contents, geminiContent{Role: "model", Parts: parts})
		default:
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			contents = append(contents, geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
				Role:  role,
			})
		}
	}
	return contents
}

// geminiTools converts Tool definitions to Gemini's functionDeclarations format.
func geminiTools(tools []core.Tool) []map[string]interface{} {
	decls := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		decls[i] = map[string]interface{}{
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"parameters":  t.Function.Parameters,
		}
	}
	return []map[string]interface{}{{"functionDeclarations": decls}}
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"contents": geminiMessages(req),
	}
	if req.System != "" {
		body["systemInstruction"] = geminiContent{
			Parts: []geminiPart{{Text: req.System}},
		}
	}
	if len(req.Tools) > 0 {
		body["tools"] = geminiTools(req.Tools)
		if req.ToolChoice != nil {
			body["tool_config"] = req.ToolChoice
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
					Text         string              `json:"text"`
					FunctionCall *geminiFunctionCall `json:"functionCall"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
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
	var toolCalls []core.ToolCall
	for _, part := range result.Candidates[0].Content.Parts {
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, core.ToolCall{
				// Gemini has no call ID; use function name so callers can round-trip via ToolCallID
				ID:   part.FunctionCall.Name,
				Type: "function",
				Function: core.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		} else {
			content += part.Text
		}
	}
	if content == "" && len(toolCalls) == 0 {
		return nil, &core.ProviderError{Provider: "gemini", Message: "empty response"}
	}

	finishReason := result.Candidates[0].FinishReason
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return &core.ChatResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Model:        model,
		Usage: core.Usage{
			InputTokens:  result.UsageMetadata.PromptTokenCount,
			OutputTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  result.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	body := map[string]interface{}{
		"contents": geminiMessages(req),
	}
	if req.System != "" {
		body["systemInstruction"] = geminiContent{
			Parts: []geminiPart{{Text: req.System}},
		}
	}
	if len(req.Tools) > 0 {
		body["tools"] = geminiTools(req.Tools)
		if req.ToolChoice != nil {
			body["tool_config"] = req.ToolChoice
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
							Text         string              `json:"text"`
							FunctionCall *geminiFunctionCall `json:"functionCall"`
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
					if pt.FunctionCall != nil {
						argsJSON, _ := json.Marshal(pt.FunctionCall.Args)
						select {
						case ch <- core.StreamChunk{
							Model: model,
							ToolCalls: []core.ToolCall{{
								ID:   pt.FunctionCall.Name,
								Type: "function",
								Function: core.FunctionCall{
									Name:      pt.FunctionCall.Name,
									Arguments: string(argsJSON),
								},
							}},
							FinishReason: "tool_calls",
						}:
						case <-ctx.Done():
							return
						}
					} else if pt.Text != "" {
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

package core

import "encoding/json"

// oaiMsg is the OpenAI wire-format message.
type oaiMsg struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`              // nil encodes as JSON null
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// OpenAIMessages converts ChatRequest messages to OpenAI wire format,
// prepending a system message when req.System is set.
func OpenAIMessages(req *ChatRequest) []oaiMsg {
	var msgs []oaiMsg
	if req.System != "" {
		s := req.System
		msgs = append(msgs, oaiMsg{Role: "system", Content: &s})
	}
	for _, m := range req.Messages {
		om := oaiMsg{Role: m.Role, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = m.ToolCalls
			if m.Content != "" {
				om.Content = &m.Content
			}
		} else {
			om.Content = &m.Content
		}
		msgs = append(msgs, om)
	}
	return msgs
}

// OpenAIBody builds the request body map for OpenAI-compatible /chat/completions.
func OpenAIBody(model string, stream bool, req *ChatRequest) map[string]interface{} {
	body := map[string]interface{}{
		"model":    model,
		"messages": OpenAIMessages(req),
	}
	if stream {
		body["stream"] = true
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		if req.ToolChoice != nil {
			body["tool_choice"] = req.ToolChoice
		}
	}
	return body
}

// OpenAIParseChat parses an OpenAI-compatible chat completion response body.
func OpenAIParseChat(data []byte, providerName string) (*ChatResponse, error) {
	var r struct {
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens      int `json:"prompt_tokens"`
			CompletionTokens  int `json:"completion_tokens"`
			TotalTokens       int `json:"total_tokens"`
			CompletionDetails *struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, &ProviderError{Provider: providerName, Message: err.Error(), Cause: err}
	}
	if len(r.Choices) == 0 {
		return nil, &ProviderError{Provider: providerName, Message: "no choices in response"}
	}
	ch := r.Choices[0]
	var content string
	if ch.Message.Content != nil {
		content = *ch.Message.Content
	}
	if content == "" && len(ch.Message.ToolCalls) == 0 {
		return nil, &ProviderError{Provider: providerName, Message: "empty response"}
	}
	reasoning := 0
	if r.Usage.CompletionDetails != nil {
		reasoning = r.Usage.CompletionDetails.ReasoningTokens
	}
	return &ChatResponse{
		Content:      content,
		ToolCalls:    ch.Message.ToolCalls,
		FinishReason: ch.FinishReason,
		Model:        r.Model,
		Usage: Usage{
			InputTokens:     r.Usage.PromptTokens,
			OutputTokens:    r.Usage.CompletionTokens,
			ReasoningTokens: reasoning,
			TotalTokens:     r.Usage.TotalTokens,
		},
	}, nil
}

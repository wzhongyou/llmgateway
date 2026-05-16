package core

import "time"

type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ChatRequest struct {
	Messages     []Message
	Model        string
	System       string
	Temperature  *float64
	MaxTokens    *int
	Stream       bool
	Tools        []Tool      `json:"tools,omitempty"`
	ToolChoice   interface{} `json:"tool_choice,omitempty"`
	ThinkingType string      `json:"thinking_type,omitempty"` // "disabled" to disable reasoning
}

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

type ChatResponse struct {
	Content          string
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	Model            string
	Provider         string
	Usage            Usage
	Latency          time.Duration
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	TotalTokens     int
}

type StreamChunk struct {
	Content      string
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"` // non-nil only on final tool_calls chunk
	FinishReason string     `json:"finish_reason,omitempty"`
	Model        string
	Usage        *Usage // non-nil only on the final chunk
	Error        error
}

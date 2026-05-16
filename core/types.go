package core

import "time"

type ChatRequest struct {
	Messages    []Message
	Model       string
	System      string
	Temperature *float64
	MaxTokens   *int
	Stream      bool
}

type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content string
}

type ChatResponse struct {
	Content  string
	Model    string
	Provider string
	Usage    Usage
	Latency  time.Duration
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	TotalTokens     int
}

type StreamChunk struct {
	Content string
	Model   string
	Usage   *Usage // non-nil only on the final chunk
	Error   error
}

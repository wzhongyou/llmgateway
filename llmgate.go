package llmgate

import (
	"github.com/wzhongyou/llmgate/core"
	"github.com/wzhongyou/llmgate/sdk"
)

type Gateway = sdk.Gateway
type ChatRequest = core.ChatRequest
type ChatResponse = core.ChatResponse
type StreamChunk = core.StreamChunk
type Message = core.Message
type Usage = core.Usage
type Tool = core.Tool
type ToolFunction = core.ToolFunction
type ToolCall = core.ToolCall
type FunctionCall = core.FunctionCall
type Provider = core.Provider
type Strategy = core.Strategy
type MetricsSnapshot = core.MetricsSnapshot
type ProviderStats = core.ProviderStats

func New() *sdk.Gateway {
	return sdk.New()
}

func NewFromFile(path string) (*sdk.Gateway, error) {
	return sdk.NewFromFile(path)
}

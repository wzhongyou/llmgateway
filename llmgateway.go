package llmgateway

import (
	"github.com/wzhongyou/llmgateway/core"
	"github.com/wzhongyou/llmgateway/sdk"
)

type Gateway = sdk.Gateway
type ChatRequest = core.ChatRequest
type ChatResponse = core.ChatResponse
type Message = core.Message
type Usage = core.Usage
type Provider = core.Provider
type Strategy = core.Strategy
type MetricsSnapshot = core.MetricsSnapshot
type ProviderStats = core.ProviderStats

func New() *sdk.Gateway {
	return sdk.New()
}

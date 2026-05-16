# llmgate Design

## Directory Structure

```
llmgate/
├── core/                 # Core: provider interface, engine, strategies, metrics, errors
│   ├── types.go          # ChatRequest, ChatResponse, Message, Usage, StreamChunk, Tool, ToolCall
│   ├── provider.go       # Provider interface
│   ├── strategy.go       # Strategy interface
│   ├── metrics.go        # MetricsSnapshot, ProviderStats
│   ├── metrics_store.go  # In-memory metrics implementation
│   ├── engine.go         # Routing engine: select → retry → fallback, circuit breaker
│   ├── errors.go         # ProviderError, MultiError
│   ├── circuit_breaker.go # Per-provider circuit breaker
│   ├── retry.go          # Exponential backoff + jitter retry
│   ├── openai.go         # Shared helpers: OpenAIMessages, OpenAIBody, OpenAIParseChat
│   ├── stream.go         # OpenAI SSE stream parser (shared, includes tool_calls accumulation)
│   ├── registry.go       # Global provider factory registry + env-var registry
│   ├── config.go         # GatewayConfig, env var expansion, validation
│   ├── strategies.go     # Built-in: PrimaryFirst, Latency, TimeBased
│   └── providers/
│       ├── openaicompat/ # All 19 OpenAI-compatible providers (data-driven)
│       │   ├── openaicompat.go  # Generic Provider implementation
│       │   └── builtins.go      # Provider definition table + init()
│       ├── anthropic/    # Anthropic (Claude) — custom Messages API
│       └── gemini/       # Google Gemini — custom generateContent API
├── sdk/                  # Go SDK: fluent API (New, NewFromFile, Use, With, Fallback, etc.)
│   ├── gateway.go        # Gateway struct + env-var loading + InitFromConfig
│   └── gateway_integration_test.go
├── server/               # HTTP server wrapping the SDK
│   ├── server.go         # HTTP handlers, middleware, auth, rate-limit, hot reload
│   └── config.go         # server.Config, ServerConfig, LoadConfig
├── llmgate.go            # Top-level type aliases + New() / NewFromFile()
├── examples/
│   ├── sdk/              # SDK example (sync + stream + tool use)
│   └── server/           # Standalone server example
└── docs/
```

---

## Protocol Families

| Protocol | Providers | Endpoint | Auth |
|----------|-----------|----------|------|
| OpenAI-compatible | baichuan, deepseek, doubao, ernie, glm, grok, groq, hunyuan, kimi, llama, mimo, minimax, mistral, openai, qwen, siliconflow, stepfun, together, yi | `POST /chat/completions` | `Bearer {key}` |
| Anthropic Messages | anthropic | `POST /messages` | `x-api-key: {key}`, `anthropic-version` header |
| Gemini generateContent | gemini | `POST /models/{model}:generateContent` | `x-goog-api-key: {key}` |

---

## Core Types

### ChatRequest / ChatResponse / StreamChunk

```go
// core/types.go
type ChatRequest struct {
    Messages    []Message
    Model       string
    System      string
    Temperature *float64
    MaxTokens   *int
    Stream      bool
    Tools       []Tool      `json:"tools,omitempty"`
    ToolChoice  interface{} `json:"tool_choice,omitempty"` // "auto" | "none" | "required"
}

type Message struct {
    Role       string     // "user" | "assistant" | "system" | "tool"
    Content    string
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // set on assistant messages
    ToolCallID string     `json:"tool_call_id,omitempty"` // set on tool result messages
}

type Tool struct {
    Type     string       // "function"
    Function ToolFunction
}

type ToolFunction struct {
    Name        string
    Description string
    Parameters  interface{} // JSON Schema object
}

type ToolCall struct {
    ID       string
    Type     string       // "function"
    Function FunctionCall
}

type FunctionCall struct {
    Name      string
    Arguments string // JSON string
}

type ChatResponse struct {
    Content      string
    ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
    FinishReason string     `json:"finish_reason,omitempty"` // "stop" | "tool_calls" | "length"
    Model        string
    Provider     string
    Usage        Usage
    Latency      time.Duration
}

type StreamChunk struct {
    Content      string
    ToolCalls    []ToolCall `json:"tool_calls,omitempty"` // non-nil only on final tool_calls chunk
    FinishReason string     `json:"finish_reason,omitempty"`
    Model        string
    Usage        *Usage  // non-nil only on the final usage chunk
    Error        error
}

type Usage struct {
    InputTokens     int
    OutputTokens    int
    ReasoningTokens int
    TotalTokens     int
}
```

### ProviderConfig

```go
// core/registry.go
type ProviderConfig struct {
    Name         string `toml:"name"`
    Key          string `toml:"key"`
    BaseURL      string `toml:"base_url"`
    DefaultModel string `toml:"default_model"`
    Protocol     string `toml:"protocol"` // "openai-compat" for user-defined providers
}
```

- `BaseURL` — optional; each provider has a built-in default. Override for proxies, private deployments, or third-party resellers.
- `DefaultModel` — optional; used when `req.Model` is empty.
- `Protocol` — set to `"openai-compat"` to use a user-defined provider not in the built-in table.

### Provider

```go
// core/provider.go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
    Models() []string
}
```

### ProviderError

```go
// core/errors.go
type ProviderError struct {
    Provider   string
    StatusCode int    // HTTP status; 0 for network/parse errors
    Message    string
    Retryable  bool   // true for 5xx, 429, network errors
    Cause      error
}
```

### Strategy

```go
// core/strategy.go
type Strategy interface {
    Select(providers []Provider, req *ChatRequest, metrics *MetricsSnapshot) []Provider
}
```

**Built-in strategies** (`core/strategies.go`):

| Strategy | Description |
|----------|-------------|
| `PrimaryFirstStrategy` | Primary → fallback list → remaining providers |
| `LatencyStrategy` | Wraps another strategy, filters providers over latency threshold |
| `TimeBasedStrategy` | Picks day/night provider by hour |

---

## Registry

```go
// core/registry.go
func RegisterProvider(name string, factory func(cfg ProviderConfig) (Provider, error))
func RegisterProviderEnv(envVar, providerName string)  // called by each provider's init()
func EnvProviders() map[string]string                  // used by sdk loadEnv()
func CreateProvider(cfg ProviderConfig) (Provider, error)
```

`CreateProvider` falls back to the `"openai-compat"` factory when `cfg.Protocol == "openai-compat"` and the name is unknown.

---

## OpenAI-compat Shared Helpers

```go
// core/openai.go
func OpenAIMessages(req *ChatRequest) []oaiMsg      // converts messages including tool/tool_calls
func OpenAIBody(model string, stream bool, req *ChatRequest) map[string]interface{}
func OpenAIParseChat(data []byte, providerName string) (*ChatResponse, error)

// core/stream.go
func OpenAIStream(ctx context.Context, body io.ReadCloser) <-chan StreamChunk
// Accumulates delta.tool_calls[] by index; emits assembled ToolCalls on [DONE]
```

---

## Engine

```go
// core/engine.go
func NewEngine(strategy Strategy) *Engine
func (e *Engine) Register(p Provider)
func (e *Engine) CreateProvider(cfg ProviderConfig) (Provider, error)
func (e *Engine) SetStrategy(s Strategy)
func (e *Engine) GetProvider(name string) (Provider, bool)
func (e *Engine) Providers() []Provider
func (e *Engine) Chat(ctx, req) (*ChatResponse, error)
func (e *Engine) ChatWithProvider(ctx, req, name) (*ChatResponse, error)
func (e *Engine) ChatWithFallback(ctx, req, names) (*ChatResponse, error)
func (e *Engine) ChatStream(ctx, req) (<-chan StreamChunk, error)
func (e *Engine) ChatStreamWithProvider(ctx, req, name) (<-chan StreamChunk, error)
func (e *Engine) Snapshot() MetricsSnapshot
```

**Reliability features:**
- **Circuit breaker** — 5 consecutive failures opens the circuit; 30s recovery timeout
- **Retry** — up to 2 attempts per provider for `Retryable` errors; exponential backoff (100ms base) with jitter
- **Error aggregation** — all provider failures collected into `*MultiError`

---

## SDK API

```go
gw := llmgate.New()                    // loads from env vars (calls core.EnvProviders())
gw, err := llmgate.NewFromFile(path)   // loads from TOML file + env var expansion
gw.Use("deepseek", key)                // register provider manually
gw.UseWithConfig(core.ProviderConfig{...})
gw.With("anthropic")                   // pin to provider
gw.Fallback("a", "b")                  // explicit chain (Chat only)
gw.UseStrategy(&MyStrategy{})
gw.Models()
gw.ProviderNames()
gw.Snapshot()
gw.Chat(ctx, req)
gw.ChatStream(ctx, req)
```

**`llmgate.New()`** reads env vars registered via `core.RegisterProviderEnv()` — automatically picks up all built-in providers' env vars without any hardcoded list.

---

## Gateway Mode

### Config

```toml
[[providers]]
name = "glm"
key = "${GLM_KEY}"

# User-defined OpenAI-compatible provider (no code required):
[[providers]]
name = "my-provider"
key = "${MY_PROVIDER_KEY}"
base_url = "https://api.my-provider.com/v1"
protocol = "openai-compat"

[strategy]
primary = "glm"
fallback = ["deepseek"]
latency_threshold_ms = 5000

[server]
listen_addr = ":8080"
api_keys = ["secret-key-1"]
rate_limit_rpm = 600
```

### HTTP Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/chat` | Chat completion — sync or SSE stream (`"stream": true`), supports tool use |
| `GET` | `/v1/models` | List available models per provider |
| `GET` | `/health` | Liveness probe |
| `GET` | `/health/ready` | Readiness probe (503 when all providers failed) |
| `GET` | `/metrics` | Provider metrics in Prometheus text format |

`/v1/chat` supports `?provider=name` and `?fallback=a&fallback=b`.

---

## Adding a Provider

### OpenAI-compatible (no code)

Add one entry to `builtins` in `core/providers/openaicompat/builtins.go`:

```go
{
    name:         "myprovider",
    baseURL:      "https://api.myprovider.com/v1",
    defaultModel: "my-model-v1",
    models:       []string{"my-model-v1", "my-model-mini"},
    envVar:       "MYPROVIDER_KEY",
},
```

This automatically registers the provider factory and its env-var mapping. No other files to touch.

### Custom API format

See [adapter-template.md](adapter-template.md).

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
├── console/              # Web console (embedded, enabled via admin_token)
│   ├── console.go        # Console struct, MockStore, mockProvider, ringBuffer
│   ├── admin.go          # Admin API handlers
│   └── static/           # Frontend (HTML/CSS/JS, embed.FS)
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
    Messages     []Message
    Model        string
    System       string
    Temperature  *float64
    MaxTokens    *int
    Stream       bool
    Tools        []Tool      `json:"tools,omitempty"`
    ToolChoice   interface{} `json:"tool_choice,omitempty"` // "auto" | "none" | "required"
    ThinkingType string      `json:"thinking_type,omitempty"` // "disabled" to disable reasoning
}

type Message struct {
    Role             string     // "user" | "assistant" | "system" | "tool"
    Content          string
    ToolCalls        []ToolCall `json:"tool_calls,omitempty"`  // set on assistant messages
    ToolCallID       string     `json:"tool_call_id,omitempty"` // set on tool result messages
    ReasoningContent string     `json:"reasoning_content,omitempty"` // DeepSeek/GLM thinking content
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
    Content          string
    ReasoningContent string     `json:"reasoning_content,omitempty"`
    ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
    FinishReason     string     `json:"finish_reason,omitempty"` // "stop" | "tool_calls" | "length"
    Model            string
    Provider         string
    Usage            Usage
    Latency          time.Duration
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
admin_token = "your-secret"
```

### HTTP Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/chat` | Chat completion — sync or SSE stream (`"stream": true`), supports tool use |
| `GET` | `/v1/models` | List available models per provider |
| `GET` | `/health` | Liveness probe |
| `GET` | `/health/live` | Liveness probe (alias) |
| `GET` | `/health/ready` | Readiness probe (503 when all providers failed) |
| `GET` | `/metrics` | Provider metrics in Prometheus text format |

`/v1/chat` supports `?provider=name` and `?fallback=a&fallback=b`.

---

## Adding a Provider

### Option A: OpenAI-compatible API (no code)

Add one entry to the `builtins` table in `core/providers/openaicompat/builtins.go`:

```go
{
    name:         "myprovider",
    baseURL:      "https://api.myprovider.com/v1",
    defaultModel: "my-model-v1",
    models:       []string{"my-model-v1", "my-model-mini"},
    envVar:       "MYPROVIDER_KEY",
},
```

That's it. The generic `Provider` in `openaicompat.go` handles `Chat`, `ChatStream`, tool use, and stream parsing automatically.

### Option B: Custom API format

For providers with a non-OpenAI wire format (like Anthropic or Gemini), create a new package.

**Step 1 — Create the package**

```
core/providers/<name>/
└── <name>.go
```

**Step 2 — Implement the Provider interface**

```go
package myprovider

import (
    "context"
    "encoding/json"
    "io"
    "net/http"

    "github.com/wzhongyou/llmgate/core"
)

const defaultBaseURL = "https://api.myprovider.com/v1"

func init() {
    core.RegisterProviderEnv("MYPROVIDER_KEY", "myprovider")
    core.RegisterProvider("myprovider", func(cfg core.ProviderConfig) (core.Provider, error) {
        baseURL := cfg.BaseURL
        if baseURL == "" {
            baseURL = defaultBaseURL
        }
        defaultModel := cfg.DefaultModel
        if defaultModel == "" {
            defaultModel = "my-model-v1"
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

func (p *Provider) Name() string     { return "myprovider" }
func (p *Provider) Models() []string { return []string{"my-model-v1", "my-model-mini"} }

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
    // Build provider-specific request, make HTTP call, parse response.
    // See anthropic.go or gemini.go for reference implementations.
    return nil, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
    return nil, nil
}
```

**Step 3 — Error handling**

Always return `*core.ProviderError`. Set `Retryable: true` for network errors, 5xx, and 429:

```go
resp, err := p.client.Do(httpReq)
if err != nil {
    return nil, &core.ProviderError{Provider: "myprovider", Message: err.Error(), Retryable: true, Cause: err}
}
if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    return nil, &core.ProviderError{
        Provider:   "myprovider",
        StatusCode: resp.StatusCode,
        Message:    string(body),
        Retryable:  resp.StatusCode >= 500 || resp.StatusCode == 429,
    }
}
```

**Step 4 — Tool use**

- **OpenAI-compatible tool format**: use `core.OpenAIBody` / `core.OpenAIParseChat` — tool use is handled automatically.
- **Anthropic format**: see `anthropicMessages()` and `anthropicTools()` in `core/providers/anthropic/anthropic.go`.
- **Gemini format**: see `geminiMessages()` and `geminiTools()` in `core/providers/gemini/gemini.go`.

**Checklist:**

- `Name()` returns a unique, lowercase identifier
- `Models()` lists supported models
- `Chat()` handles `req.System`, `req.Model`, `req.MaxTokens`, `req.Temperature`
- `Chat()` handles `req.Tools` and `req.ToolChoice`; populates `resp.ToolCalls`
- `Chat()` uses `baseURL` from config if set, falls back to built-in default
- `Chat()` parses reasoning content when available
- `Chat()` returns `*core.ProviderError` with correct `Retryable` flag
- `ChatStream()` implemented; returns error before channel is opened
- `init()` calls `core.RegisterProviderEnv(envVar, name)` then `core.RegisterProvider(name, factory)`

---

## Console

The gateway binary embeds a developer web console. Set `admin_token` to enable it at `/admin/`.

### Directory

```
console/
├── console.go         # Console struct, MockStore, mockProvider, ringBuffer
├── admin.go           # Admin API handlers + Setup
└── static/            # Embedded frontend (embed.FS)
    ├── index.html
    ├── app.js
    └── style.css
```

### Admin API

```
GET    /admin/api/channels              # list providers with metrics
GET    /admin/api/channels/{name}       # get single provider detail
PUT    /admin/api/channels/{name}       # create or update provider
DELETE /admin/api/channels/{name}       # remove provider
POST   /admin/api/channels/{name}/test  # test connection

POST   /admin/api/playground/chat       # sync chat
POST   /admin/api/playground/stream     # SSE streaming chat

GET    /admin/api/mock/rules            # list mock rules
POST   /admin/api/mock/rules            # create mock rule
PUT    /admin/api/mock/rules/{id}       # update mock rule
DELETE /admin/api/mock/rules/{id}       # delete mock rule
POST   /admin/api/mock/rules/reorder    # reorder priorities

GET    /admin/api/recent                # list recent requests
GET    /admin/api/recent/{id}           # get full request/response

POST   /admin/api/config/save           # write config to TOML file
```

### Mock Provider

Registered as `"mock"` on the engine. Each `MockRule` has a match model, priority, and action (`response`/`error`/`timeout`/`empty`). Preset templates include 429, 500, timeout, and empty response. Rules take effect immediately — no restart required.

### Recent Requests

Last 200 requests in a ring buffer (in-memory, lost on restart). Summary table auto-refreshes every 5 seconds. Expand any entry to view full request/response JSON.

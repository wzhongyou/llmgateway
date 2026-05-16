# llmgate Design

## Directory Structure

```
llmgate/
├── core/                 # Core: provider interface, engine, strategies, metrics, errors
│   ├── types.go          # ChatRequest, ChatResponse, Message, Usage, StreamChunk
│   ├── provider.go       # Provider interface
│   ├── strategy.go       # Strategy interface
│   ├── metrics.go        # MetricsSnapshot, ProviderStats
│   ├── metrics_store.go  # In-memory metrics implementation
│   ├── engine.go         # Routing engine: select → retry → fallback, circuit breaker
│   ├── errors.go         # ProviderError, MultiError
│   ├── circuit_breaker.go # Per-provider circuit breaker
│   ├── retry.go          # Exponential backoff + jitter retry
│   ├── stream.go         # OpenAI SSE stream parser (shared by 12 providers)
│   ├── registry.go       # Global provider factory registry (RegisterProvider / CreateProvider)
│   ├── config.go         # GatewayConfig, env var expansion, validation
│   ├── strategies.go     # Built-in: PrimaryFirst, Latency, TimeBased
│   └── providers/        # Provider adapters (one directory per provider)
│       ├── anthropic/    # Anthropic (Claude) — custom Messages API
│       ├── deepseek/     # DeepSeek — OpenAI-compatible
│       ├── ernie/        # Baidu ERNIE — OpenAI-compatible
│       ├── gemini/       # Google Gemini — custom generateContent API
│       ├── glm/          # Zhipu GLM — OpenAI-compatible
│       ├── grok/         # xAI Grok — OpenAI-compatible
│       ├── hunyuan/      # Tencent Hunyuan — OpenAI-compatible
│       ├── kimi/         # Moonshot Kimi — OpenAI-compatible
│       ├── llama/        # Meta Llama — OpenAI-compatible
│       ├── mimo/         # Xiaomi MiMo — OpenAI-compatible
│       ├── minimax/      # MiniMax — OpenAI-compatible
│       ├── openai/       # OpenAI — OpenAI-compatible
│       ├── qwen/         # Alibaba Qwen — OpenAI-compatible
│       └── stepfun/      # StepFun — OpenAI-compatible
├── sdk/                  # Go SDK: fluent API (New, NewFromFile, Use, With, Fallback, etc.)
│   ├── gateway.go        # Gateway struct + env-var loading + InitFromConfig
│   └── gateway_integration_test.go
├── server/               # HTTP server wrapping the SDK
│   ├── server.go         # HTTP handlers, middleware, auth, rate-limit, hot reload
│   └── config.go         # server.Config, ServerConfig, LoadConfig
├── llmgate.go            # Top-level type aliases + New() / NewFromFile()
├── examples/             # Usage examples
│   ├── sdk/              # SDK example (sync + stream)
│   └── server/           # Standalone server example
└── docs/                 # Documentation
```

---

## Protocol Families

Providers are categorized by the API protocol they speak. The `Provider` interface abstracts all of them.

| Protocol | Providers | Endpoint | Auth |
|----------|-----------|----------|------|
| OpenAI-compatible | deepseek, ernie, glm, grok, hunyuan, kimi, llama, mimo, minimax, openai, qwen, stepfun | `POST /chat/completions` | `Bearer {key}` |
| Anthropic Messages | anthropic | `POST /messages` | `x-api-key: {key}`, `anthropic-version` header |
| Gemini generateContent | gemini | `POST /models/{model}:generateContent` | `x-goog-api-key: {key}` |

---

## Core Interfaces

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
```

### ProviderConfig

```go
// core/registry.go
type ProviderConfig struct {
    Name         string `toml:"name"`
    Key          string `toml:"key"`
    BaseURL      string `toml:"base_url"`
    DefaultModel string `toml:"default_model"`
}
```

- `BaseURL` — optional; each provider has a built-in default. Override for proxies, private deployments, or third-party resellers.
- `DefaultModel` — optional; used when `req.Model` is empty.

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

Each provider is registered via `init()` in its own package:

```go
func init() {
    core.RegisterProvider("deepseek", func(cfg core.ProviderConfig) (core.Provider, error) {
        ...
        return &Provider{key: cfg.Key, baseURL: baseURL, defaultModel: defaultModel}, nil
    })
}
```

### ProviderError

All providers return `*ProviderError` for structured error handling:

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

`MultiError` aggregates failures from multiple providers and implements `errors.Unwrap() []error`.

### Strategy

`Select` returns an **ordered** provider list. The engine tries them in sequence until one succeeds.

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
| `TimeBasedStrategy` | Picks day/night provider by hour; `SetNowFn` for testability |

### MetricsSnapshot / ProviderStats

```go
// core/metrics.go
type MetricsSnapshot struct {
    Providers map[string]ProviderStats
}

type ProviderStats struct {
    TotalCalls   int64
    ErrorCalls   int64
    ErrorRate    float64
    AvgLatencyMs float64
    Available    bool  // false when error rate == 1.0
}
```

---

## Engine

```go
// core/engine.go
func NewEngine(strategy Strategy) *Engine
func (e *Engine) Register(p Provider)
func (e *Engine) RegisterFactory(name string, factory func(ProviderConfig) (Provider, error))
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
- **Circuit breaker** — 5 consecutive failures opens the circuit; 30s recovery timeout; per-provider state stored in `Engine.breakers`
- **Retry** — up to 2 attempts per provider for `Retryable` errors; exponential backoff (100ms base) with jitter
- **Error aggregation** — all provider failures collected into `*MultiError`

`RegisterFactory` registers a local factory override on this engine instance, used to inject mock providers in unit tests without touching the global registry.

---

## SDK API

```go
gw := llmgate.New()                    // loads from env vars only
gw, err := llmgate.NewFromFile(path)   // loads from TOML file + env var expansion
gw.Use("deepseek", key)                // register provider manually (returns error)
gw.UseWithConfig(core.ProviderConfig{...})
gw.With("anthropic")                   // pin to provider
gw.Fallback("a", "b")                  // explicit chain (Chat only; ignored by ChatStream)
gw.UseStrategy(&MyStrategy{})          // custom strategy
gw.Models()                            // list all model IDs across providers
gw.ProviderNames()                     // list registered provider names
gw.Snapshot()                          // metrics snapshot
gw.Chat(ctx, req)                      // blocking call → *ChatResponse
gw.ChatStream(ctx, req)               // streaming → <-chan StreamChunk
```

**`llmgate.New()`** reads only environment variables:
`ANTHROPIC_KEY`, `DEEPSEEK_KEY`, `ERNIE_KEY`, `GEMINI_KEY`, `GLM_KEY`, `GROK_KEY`, `HUNYUAN_KEY`, `KIMI_KEY`, `LLAMA_KEY`, `MIMO_KEY`, `MINIMAX_KEY`, `OPENAI_KEY`, `QWEN_KEY`, `STEPFUN_KEY`

**`llmgate.NewFromFile(path)`** reads a TOML config file with `${VAR}` env expansion.

**Call precedence for `Chat` (highest to lowest):**
1. `.Fallback(...)` — explicit in-code chain
2. `.With(...)` — pin to a specific provider
3. `UseStrategy(...)` — custom strategy
4. Default built-in strategy (primary → fallback from config)
5. Registration order (no strategy configured)

**`ChatStream` precedence:** same as above except `.Fallback(...)` is not supported — stream fallback cannot happen after the channel is opened.

---

## Gateway Mode

### Config (`server.Config`)

```go
// server/config.go
type Config struct {
    Providers []core.ProviderConfig `toml:"providers"`
    Strategy  core.StrategyConfig   `toml:"strategy"`
    Server    ServerConfig          `toml:"server"`
}

type ServerConfig struct {
    ListenAddr   string   `toml:"listen_addr"`
    APIKeys      []string `toml:"api_keys"`      // Bearer token auth; empty = no auth
    RateLimitRPM int      `toml:"rate_limit_rpm"` // global RPM; 0 = unlimited
}
```

TOML example:

```toml
[[providers]]
name = "glm"
key = "${GLM_KEY}"

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
| `POST` | `/v1/chat` | Chat completion — sync or SSE stream (`"stream": true`) |
| `GET` | `/v1/models` | List available models per provider |
| `GET` | `/health` | Liveness probe (always 200) |
| `GET` | `/health/live` | Liveness probe (always 200) |
| `GET` | `/health/ready` | Readiness probe (503 when all providers failed) |
| `GET` | `/metrics` | Provider metrics in Prometheus text format |

`/v1/chat` supports `?provider=name` to pin a provider and `?fallback=a&fallback=b` for an explicit chain.

### Streaming

Set `"stream": true` in the request body. The response is an SSE stream:

```
data: {"Content":"Hello","Model":"deepseek-v4-flash"}
data: {"Content":" world"}
data: {"Content":"","Usage":{"InputTokens":5,"OutputTokens":10,...}}
data: [DONE]
```

### Observability

Every request logs one structured JSON line. The chat endpoint includes LLM-specific fields:

```json
{"level":"INFO","msg":"request","request_id":"a3f2...","method":"POST","path":"/v1/chat",
 "status":200,"latency_ms":312.5,"remote_addr":"...",
 "provider":"glm","model":"glm-5.1","input_tokens":15,"output_tokens":42,"reasoning_tokens":0}
```

`GET /metrics` returns Prometheus text exposition format:

```
# TYPE llmgate_requests_total counter
llmgate_requests_total{provider="glm"} 42
# TYPE llmgate_errors_total counter
llmgate_errors_total{provider="glm"} 1
# TYPE llmgate_provider_avg_latency_ms gauge
llmgate_provider_avg_latency_ms{provider="glm"} 312.500
# TYPE llmgate_provider_available gauge
llmgate_provider_available{provider="glm"} 1
```

### Hot Reload

`server.WatchConfig(ctx, cfgPath)` starts a background goroutine that polls the config file every 10 seconds. When the file's mtime changes, it atomically swaps the gateway instance — no restart required.

---

## Adding a Provider

1. Create a directory under `core/providers/<name>/`
2. Implement the `Provider` interface (including `ChatStream`)
3. Register via `init()`:
   ```go
   func init() {
       core.RegisterProvider("name", func(cfg core.ProviderConfig) (core.Provider, error) {
           return &Provider{key: cfg.Key}, nil
       })
   }
   ```
4. Import the package with `_` in the consuming code
5. Add the provider to the env-var map in `sdk/gateway.go` `loadEnv()`
6. See [adapter-template.md](adapter-template.md) for a complete example

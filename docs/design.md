# llmgate Design

## Directory Structure

```
llmgate/
├── core/                 # Core: provider interface, engine, strategies, metrics
│   ├── types.go          # ChatRequest, ChatResponse, Message, Usage
│   ├── provider.go       # Provider interface
│   ├── strategy.go       # Strategy interface
│   ├── metrics.go        # MetricsSnapshot, ProviderStats
│   ├── metrics_store.go  # In-memory metrics implementation
│   ├── engine.go         # Routing engine: select → try → fallback
│   ├── registry.go       # Provider registry (RegisterProvider / CreateProvider)
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
├── sdk/                  # Go SDK: fluent API (New, Use, With, Fallback, etc.)
│   └── gateway.go        # Gateway struct + auto-load logic
├── gateway/              # HTTP server wrapping core
│   ├── server.go         # HTTP handlers, middleware, structured logging
│   └── config.go         # TOML config loader
├── llmgate.go         # Top-level type aliases + New()
├── examples/             # Usage examples
│   ├── sdk/              # SDK example
│   └── gateway/          # Standalone gateway example
└── docs/                 # Documentation
```

---

## Protocol Families

Providers are categorized by the API protocol they speak. The `Provider` interface abstracts all of them into a single `Chat(ctx, req) (*ChatResponse, error)` method.

| Protocol | Providers | Endpoint | Auth |
|----------|-----------|----------|------|
| OpenAI-compatible | deepseek, ernie, glm, grok, hunyuan, kimi, llama, mimo, minimax, openai, qwen, stepfun | `POST /chat/completions` | `Bearer {key}` |
| Anthropic Messages | anthropic | `POST /messages` | `x-api-key: {key}`, `anthropic-version` header, system as top-level field |
| Gemini generateContent | gemini | `POST /models/{model}:generateContent` | `x-goog-api-key: {key}`, model in URL path |

---

## Core Interfaces

### ChatRequest / ChatResponse / Message / Usage

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

- `BaseURL` — optional; each provider has a built-in default. Override for proxies, private deployments, or third-party resellers (e.g. GLM via Alibaba Bailian).
- `DefaultModel` — optional; used when `req.Model` is empty. Each provider has a built-in default.

### Provider

```go
// core/provider.go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    Models() []string
}
```

Each provider is registered in `core/registry.go` via `init()`:

```go
// core/providers/deepseek/deepseek.go
func init() {
    core.RegisterProvider("deepseek", func(cfg core.ProviderConfig) (core.Provider, error) {
        baseURL := cfg.BaseURL
        if baseURL == "" {
            baseURL = defaultBaseURL
        }
        defaultModel := cfg.DefaultModel
        if defaultModel == "" {
            defaultModel = "deepseek-v4-flash"
        }
        return &Provider{
            key:          cfg.Key,
            baseURL:      baseURL,
            defaultModel: defaultModel,
        }, nil
    })
}
```

### Strategy

`Select` returns an **ordered** provider list. The engine tries them in sequence until one succeeds — this naturally supports fallback chains without re-invoking the strategy on each failure.

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
| `TimeBasedStrategy` | Picks day/night provider based on current hour |

Strategies can be stacked (e.g., `LatencyStrategy` wrapping `PrimaryFirstStrategy`).

### MetricsSnapshot / ProviderStats

```go
// core/metrics.go
type MetricsSnapshot struct {
    Providers map[string]ProviderStats
}

type ProviderStats struct {
    ErrorRate    float64
    AvgLatencyMs float64
    Available    bool
}
```

---

## Engine

```go
// core/engine.go
type Engine struct { ... }

func NewEngine(strategy Strategy) *Engine
func (e *Engine) Register(p Provider)
func (e *Engine) SetStrategy(s Strategy)
func (e *Engine) Chat(ctx, req) (*ChatResponse, error)      // strategy-driven
func (e *Engine) ChatWithProvider(ctx, req, name)            // pinned
func (e *Engine) ChatWithFallback(ctx, req, names)           // explicit chain
func (e *Engine) Snapshot() MetricsSnapshot
```

The engine records per-provider metrics (success/failure, latency) on every call.

---

## SDK API

```go
gw := llmgate.New()       // auto-loads llmgate.toml or env vars
gw.Use("deepseek", "...")    // manual registration
gw.With("anthropic")         // pin to provider
gw.Fallback("a", "b")        // explicit chain
gw.UseStrategy(&MyStrategy{}) // custom strategy
```

**Auto-load behavior** (`sdk/gateway.go`):

1. Read `llmgate.toml` from CWD (TOML format, with `${ENV}` expansion)
2. Fallback: scan env vars (`ANTHROPIC_KEY`, `DEEPSEEK_KEY`, `ERNIE_KEY`, `GEMINI_KEY`, `GLM_KEY`, `GROK_KEY`, `HUNYUAN_KEY`, `KIMI_KEY`, `LLAMA_KEY`, `MIMO_KEY`, `MINIMAX_KEY`, `OPENAI_KEY`, `QWEN_KEY`, `STEPFUN_KEY`)
3. If neither found, gateway starts empty — user calls `gw.Use()` manually

**Precedence (highest to lowest):**
1. `.Fallback(...)` — explicit in-code chain
2. `.With(...)` — pin to a specific provider
3. `UseStrategy(...)` — custom strategy
4. Default built-in strategy (primary → fallback list from config)
5. Registration order (no strategy configured)

---

## Gateway Mode

### Config

TOML format with `${VAR}` environment variable substitution (handled by `core/config.go` at startup):

```toml
[[providers]]
name = "glm"
key = "${GLM_KEY}"
default_model = "glm-5.1"
# base_url = "https://open.bigmodel.cn/api/paas/v4"

[[providers]]
name = "deepseek"
key = "${DEEPSEEK_KEY}"
default_model = "deepseek-v4-flash"

[strategy]
primary = "glm"
fallback = ["deepseek"]
latency_threshold_ms = 5000

[server]
listen_addr = ":8080"
```

### HTTP Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/chat` | Chat completion (supports `?provider=` and `?fallback=`) |
| `GET` | `/v1/models` | List available models |
| `GET` | `/health` | Health check |

### Request/Response

```json
// POST /v1/chat
{
  "messages": [{"role": "user", "content": "Hello"}],
  "model": "deepseek-v4-flash",
  "system": "You are helpful.",
  "temperature": 0.7,
  "max_tokens": 1024
}

// Response
{
  "content": "Hello! How can I help?",
  "model": "deepseek-v4-flash",
  "provider": "deepseek",
  "usage": {
    "input_tokens": 10,
    "output_tokens": 5,
    "reasoning_tokens": 0,
    "total_tokens": 15
  },
  "latency": 1234567890
}
```

### Observability

Every request is logged as a single structured JSON line via `log/slog`. The chat endpoint includes LLM-specific fields:

```json
{"level":"INFO","msg":"request","request_id":"...","method":"POST","path":"/v1/chat",
 "status":200,"latency_ms":312.5,"remote_addr":"...",
 "provider":"glm","model":"glm-5.1","input_tokens":15,"output_tokens":42,
 "reasoning_tokens":0}
```

Inject a custom logger via `gateway.WithLogger(l *slog.Logger)`.

---

## Metrics Storage

| Mode | Storage | Notes |
|------|---------|-------|
| SDK | In-memory | per-process, lost on restart |
| Gateway (single node) | SQLite | zero extra dependencies (planned) |
| Gateway (multi-node) | Redis | configure via `[metrics] backend = "redis"` (planned) |

Currently only in-memory is implemented (`core/metrics_store.go`). Access via `gw.Snapshot()`.

---

## Adding a Provider

1. Create a directory under `core/providers/<name>/`
2. Implement the `Provider` interface
3. Register via `init()`:
   ```go
   func init() {
       core.RegisterProvider("name", func(cfg core.ProviderConfig) (core.Provider, error) {
           return &Provider{key: cfg.Key}, nil
       })
   }
   ```
4. Import the package with `_` in the consuming code
5. Add the provider to the env-var map in `sdk/gateway.go`
6. See [adapter-template.md](adapter-template.md) for a complete example

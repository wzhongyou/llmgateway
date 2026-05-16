# llmgate

> LLM infrastructure layer for Go agent applications

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.21-blue)](https://golang.org/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

[中文文档](README_zh.md) · [Design Doc](docs/design.md) · [Contributing](CONTRIBUTING.md)

---

## What is this

You're building agent applications and need to integrate multiple LLMs. Three real problems:

- **Switching models requires code changes** — every provider has a different API, business logic gets coupled to models
- **When things go wrong, you don't know why** — is the model slow? Rate limited? Token budget exceeded?
- **Token usage is opaque** — mixing multiple providers, input/output/reasoning breakdowns don't add up

**llmgate** addresses this: a unified interface that hides provider differences, white-box logging of provider / model / token breakdown / latency on every call, built-in fallback and latency-limit strategies, laying the groundwork for visual monitoring.

**Three usage modes — pick what fits:**

| Mode | Use case |
|------|----------|
| **SDK** | Import directly in Go projects, integrate multiple models in one line |
| **Gateway** | Deploy as a standalone HTTP service, works with any language |
| **Studio** (planned) | Visual console — latency distribution, model comparison, token trends in one view |

---

## Quick Start

```bash
go get github.com/wzhongyou/llmgate
```

**Pick one of three ways to configure:**

**Option 1 — Config file (recommended)**

```bash
cp llmgate.toml.example llmgate.toml
# edit the key field
```

```go
gw, err := llmgate.NewFromFile("llmgate.toml")
```

**Option 2 — Environment variable**

```bash
export DEEPSEEK_KEY="sk-xxx"
export GLM_KEY="your-glm-key"
```

```go
gw := llmgate.New()  // auto-loads from env vars
```

**Option 3 — Code**

```go
gw := llmgate.New()
gw.Use("deepseek", "sk-xxx")
```

**Then use it:**

```go
package main

import (
    "context"
    "fmt"

    "github.com/wzhongyou/llmgate"

    // One blank import registers all 21 built-in providers
    _ "github.com/wzhongyou/llmgate/core/providers/openaicompat"
    _ "github.com/wzhongyou/llmgate/core/providers/anthropic"
    _ "github.com/wzhongyou/llmgate/core/providers/gemini"
)

func main() {
    gw, err := llmgate.NewFromFile("llmgate.toml")
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    reply, err := gw.Chat(ctx, &llmgate.ChatRequest{
        Messages: []llmgate.Message{
            {Role: "user", Content: "Write a Go HTTP server"},
        },
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("[%s] %s\n", reply.Provider, reply.Content)
}
```

---

## API

```go
gw := llmgate.New()

// Register providers
gw.Use("deepseek", "sk-xxx")
gw.Use("anthropic", "sk-xxx")

// Pin to a provider
reply, _ := gw.With("anthropic").Chat(ctx, req)

// Fallback chain
reply, _ := gw.Fallback("anthropic", "deepseek").Chat(ctx, req)

// Streaming (SSE)
ch, err := gw.ChatStream(ctx, &llmgate.ChatRequest{
    Messages: []llmgate.Message{{Role: "user", Content: "Hello"}},
})
for chunk := range ch {
    if chunk.Error != nil { break }
    fmt.Print(chunk.Content)
}

// Function calling (tool use)
reply, _ := gw.Chat(ctx, &llmgate.ChatRequest{
    Messages: []llmgate.Message{{Role: "user", Content: "What's the weather in Beijing?"}},
    Tools: []llmgate.Tool{{
        Type: "function",
        Function: llmgate.ToolFunction{
            Name:        "get_weather",
            Description: "Get current weather for a city",
            Parameters: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "city": map[string]interface{}{"type": "string"},
                },
                "required": []string{"city"},
            },
        },
    }},
    ToolChoice: "auto",
})
// reply.ToolCalls contains the model's tool invocation
// reply.FinishReason == "tool_calls"

// Metrics
snap := gw.Snapshot()
fmt.Printf("DeepSeek latency: %.2f ms\n", snap.Providers["deepseek"].AvgLatencyMs)
```

**Precedence (highest to lowest):**
1. `.Fallback(...)` — explicit in-code chain
2. `.With(...)` — pin to a provider
3. `UseStrategy(...)` — custom strategy
4. Auto-detect (llmgate.toml → env vars → code)

---

## Server Mode

Standalone HTTP server for multi-language access:

```bash
cp llmgate.toml.example llmgate.toml
go run examples/server/main.go
```

```toml
# llmgate.toml
[[providers]]
name = "glm"
key = "${GLM_KEY}"

[[providers]]
name = "deepseek"
key = "${DEEPSEEK_KEY}"

[strategy]
primary = "glm"
fallback = ["deepseek"]
latency_threshold_ms = 5000

[server]
listen_addr = ":8080"
```

Endpoints:
- `POST /v1/chat` — chat completion (supports function calling; optional `?provider=` / `?fallback=` query params)
- `GET /v1/models` — list available models
- `GET /health` — health check

---

## Observability

Every `/v1/chat` request emits one structured JSON log line:

```json
{"time":"...","level":"INFO","msg":"request",
 "request_id":"1747123456789","method":"POST","path":"/v1/chat",
 "status":200,"latency_ms":312.5,"remote_addr":"127.0.0.1:54321",
 "provider":"glm","model":"glm-5.1",
 "input_tokens":15,"output_tokens":42,"reasoning_tokens":0}
```

Inject a custom logger:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
srv, _ := server.New(cfg, server.WithLogger(logger))
```

---

## Supported Providers

| Provider | Protocol | Default Model |
|----------|----------|---------------|
| Anthropic (Claude) | Anthropic Messages API | `claude-sonnet-4-6` |
| Baichuan | OpenAI-compatible | `Baichuan4` |
| Baidu (ERNIE) | OpenAI-compatible | `ernie-5.1` |
| ByteDance (Doubao) | OpenAI-compatible | `doubao-seed-1.6-250615` |
| DeepSeek | OpenAI-compatible | `deepseek-v4-flash` |
| Google (Gemini) | Gemini generateContent | `gemini-3.1-flash` |
| Groq | OpenAI-compatible | `llama-3.3-70b-versatile` |
| Meta (Llama) | OpenAI-compatible | `llama-4-maverick` |
| MiniMax | OpenAI-compatible | `MiniMax-M2.7` |
| Mistral | OpenAI-compatible | `mistral-large-latest` |
| Moonshot (Kimi) | OpenAI-compatible | `kimi-k2.6` |
| OpenAI | OpenAI-compatible | `gpt-5.5` |
| Qwen (Alibaba Bailian) | OpenAI-compatible | `qwen3.6-plus` |
| SiliconFlow | OpenAI-compatible | `Qwen/Qwen2.5-72B-Instruct` |
| StepFun | OpenAI-compatible | `step-3.5-flash` |
| Tencent (Hunyuan) | OpenAI-compatible | `hy3-preview` |
| Together AI | OpenAI-compatible | `meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo` |
| xAI (Grok) | OpenAI-compatible | `grok-4.1-fast-non-reasoning` |
| Xiaomi (MiMo) | OpenAI-compatible | `mimo-v2-pro` |
| Yi (01.AI) | OpenAI-compatible | `yi-large` |
| Zhipu (GLM) | OpenAI-compatible | `glm-5.1` |

**3 protocol families**: OpenAI-compatible (19 providers), Anthropic Messages, Gemini generateContent.

All providers support `base_url` override. To add any other OpenAI-compatible provider without writing code, use `protocol = "openai-compat"` in config:

```toml
[[providers]]
name = "my-provider"
key = "${MY_PROVIDER_KEY}"
base_url = "https://api.my-provider.com/v1"
protocol = "openai-compat"
```

---

## Project Structure

```
llmgate/
├── core/                 # Provider interface, engine, strategies, metrics
│   └── providers/
│       ├── openaicompat/ # All 19 OpenAI-compatible providers (data-driven)
│       ├── anthropic/    # Anthropic Messages API
│       └── gemini/       # Gemini generateContent API
├── sdk/                  # Go SDK
├── server/               # HTTP server
├── docs/                 # Design docs
└── examples/             # Usage examples
```

---

## Testing

```bash
# 1. Configure your keys
cp llmgate.toml.example llmgate.toml
# Fill in real keys, or set env vars:
# export GLM_KEY=xxx  MINIMAX_KEY=xxx  DEEPSEEK_KEY=xxx

# 2. Run integration tests
go test ./sdk/ ./server/ -v -count=1
```

Tests skip automatically if no key is configured.

---

## Roadmap

- [x] **v0.1** — Go SDK + DeepSeek + basic fallback strategy + metrics
- [x] **v0.2** — Zhipu (GLM) + MiniMax + structured logging (slog)
- [x] **v0.3** — 14 providers across 3 protocols, reasoning tokens, configurable default models
- [x] **v1.0** — Streaming (SSE) + production routing (circuit breaking, rate limiting, retry)
- [x] **v1.1** — Function calling (tool use) across all providers; 21 providers; data-driven provider architecture
- [ ] **v1.5** — Visual console: latency distribution, prompt version management, model evaluation

---

## Adding a Provider

**For OpenAI-compatible APIs** — add one entry to the `builtins` table in [core/providers/openaicompat/builtins.go](core/providers/openaicompat/builtins.go):

```go
{
    name:         "myprovider",
    baseURL:      "https://api.myprovider.com/v1",
    defaultModel: "my-model",
    models:       []string{"my-model"},
    envVar:       "MYPROVIDER_KEY",
},
```

**For custom API formats** — implement the `Provider` interface (see [adapter-template.md](docs/adapter-template.md)).

---

## License

[MIT](LICENSE)

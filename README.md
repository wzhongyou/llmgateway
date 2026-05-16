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

    // Step 1: blank-import providers to register them
    _ "github.com/wzhongyou/llmgate/core/providers/deepseek"
    _ "github.com/wzhongyou/llmgate/core/providers/glm"
)

func main() {
    // Step 2: create gateway (pick one)
    gw, err := llmgate.NewFromFile("llmgate.toml")
    if err != nil {
        panic(err)
    }

    // Step 3: chat
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
if err != nil {
    return
}
for chunk := range ch {
    if chunk.Error != nil {
        fmt.Println("stream error:", chunk.Error)
        return
    }
    fmt.Print(chunk.Content)
}

// Metrics
snap := gw.Snapshot()
fmt.Printf("DeepSeek latency: %.2f ms\n", snap.Providers["deepseek"].AvgLatencyMs)

// Custom strategy (requires importing core)
// gw.UseStrategy(&core.PrimaryFirstStrategy{...})
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

Endpoints:
- `POST /v1/chat` — chat completion (optional `?provider=` / `?fallback=` query params)
- `GET /v1/models` — list available models
- `GET /health` — health check

---

## Observability

Every `/v1/chat` request emits one structured JSON log line capturing performance and token breakdown:

```json
{"time":"...","level":"INFO","msg":"request",
 "request_id":"1747123456789","method":"POST","path":"/v1/chat",
 "status":200,"latency_ms":312.5,"remote_addr":"127.0.0.1:54321",
 "provider":"glm","model":"glm-5.1",
 "input_tokens":15,"output_tokens":42,"reasoning_tokens":0}
```

Inject a custom logger in Gateway mode:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
srv, _ := server.New(cfg, server.WithLogger(logger))
```

---

## Supported Providers

| Provider | Protocol | Default Model |
|----------|----------|---------------|
| Anthropic (Claude) | Anthropic Messages API | `claude-sonnet-4-6` |
| Baidu (ERNIE) | OpenAI-compatible | `ernie-5.1` |
| DeepSeek | OpenAI-compatible | `deepseek-v4-flash` |
| Google (Gemini) | Gemini generateContent | `gemini-3.1-flash` |
| Meta (Llama) | OpenAI-compatible | `llama-4-maverick` |
| MiniMax | OpenAI-compatible | `MiniMax-M2.7` |
| Moonshot (Kimi) | OpenAI-compatible | `kimi-k2.6` |
| OpenAI | OpenAI-compatible | `gpt-5.5` |
| Qwen (Alibaba Bailian) | OpenAI-compatible | `qwen3.6-plus` |
| StepFun | OpenAI-compatible | `step-3.5-flash` |
| Tencent (Hunyuan) | OpenAI-compatible | `hy3-preview` |
| xAI (Grok) | OpenAI-compatible | `grok-4.1-fast-non-reasoning` |
| Xiaomi (MiMo) | OpenAI-compatible | `mimo-v2-pro` |
| Zhipu (GLM) | OpenAI-compatible | `glm-5.1` |

**3 protocol families**: OpenAI-compatible (12 providers), Anthropic Messages, Gemini generateContent.

All providers support `base_url` override for proxies, private deployments, or third-party resellers.

---

## Project Structure

```
llmgate/
├── core/        # Provider interface, engine, strategies, metrics
├── sdk/         # Go SDK
├── server/      # HTTP server
├── docs/        # Design docs
└── examples/    # Usage examples
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
- [ ] **v1.5** — Visual console: latency distribution, prompt version management, model evaluation

---

## Adding a Provider

1. Implement the `Provider` interface (see [adapter-template.md](docs/adapter-template.md))
2. Register via `init()`:
   ```go
   func init() {
       core.RegisterProvider("name", factory)
   }
   ```
3. Add to the env-var map in `sdk/gateway.go`
4. Send a PR with tests

---

## License

[MIT](LICENSE)

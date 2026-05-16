# Provider Adapter Template

This guide covers adding a new LLM provider to llmgate.

---

## Option A: OpenAI-compatible API (no new files)

If the provider speaks the OpenAI `/chat/completions` format, add one entry to the `builtins` table in `core/providers/openaicompat/builtins.go`:

```go
{
    name:         "myprovider",
    baseURL:      "https://api.myprovider.com/v1",
    defaultModel: "my-model-v1",
    models:       []string{"my-model-v1", "my-model-mini"},
    envVar:       "MYPROVIDER_KEY",
},
```

That's it. The generic `Provider` in `openaicompat.go` handles `Chat`, `ChatStream`, tool use, and all error handling automatically.

---

## Option B: Custom API format

For providers with a non-OpenAI wire format (like Anthropic or Gemini), create a new package.

### Step 1: Create the package

```
core/providers/<name>/
└── <name>.go
```

### Step 2: Implement the Provider interface

```go
package myprovider

import (
    "bytes"
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

func (p *Provider) Name() string { return "myprovider" }

func (p *Provider) Models() []string {
    return []string{"my-model-v1", "my-model-mini"}
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
    // Build provider-specific request, make HTTP call, parse response
    // See anthropic.go or gemini.go for reference
    return nil, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
    // If OpenAI-compatible streaming: return core.OpenAIStream(ctx, resp.Body)
    // For custom SSE: parse in a goroutine, see anthropic.go for reference
    return nil, nil
}
```

### Step 3: Error handling

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

### Step 4: Tool use

- **OpenAI-compatible tool format**: use `core.OpenAIBody` / `core.OpenAIParseChat` — tool use is handled automatically.
- **Anthropic format**: see `anthropicMessages()` and `anthropicTools()` in `core/providers/anthropic/anthropic.go`.
- **Gemini format**: see `geminiMessages()` and `geminiTools()` in `core/providers/gemini/gemini.go`.

### Step 5: Consume the package

```go
import _ "github.com/wzhongyou/llmgate/core/providers/myprovider"
```

The `RegisterProviderEnv` call in `init()` means `llmgate.New()` will automatically pick up `MYPROVIDER_KEY` from the environment.

---

## Checklist

- [ ] `Name()` returns a unique, lowercase identifier
- [ ] `Models()` lists supported models
- [ ] `Chat()` handles `req.System`, `req.Model`, `req.MaxTokens`, `req.Temperature`
- [ ] `Chat()` handles `req.Tools` and `req.ToolChoice`; populates `resp.ToolCalls` and `resp.FinishReason`
- [ ] `Chat()` uses `baseURL` from config if set, falls back to built-in default
- [ ] `Chat()` parses reasoning tokens when available
- [ ] `Chat()` passes context to HTTP request
- [ ] `Chat()` returns `*core.ProviderError` for all errors (correct `Retryable` flag)
- [ ] `ChatStream()` implemented; returns `*core.ProviderError` before channel is opened
- [ ] `init()` calls `core.RegisterProviderEnv(envVar, name)` then `core.RegisterProvider(name, factory)`

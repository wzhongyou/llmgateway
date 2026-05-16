# Provider Adapter Template

This is a step-by-step guide for adding a new LLM provider to llmgate.

---

## Step 1: Create the directory

```
core/providers/<name>/
└── <name>.go
```

Example: `core/providers/openai/openai.go`

---

## Step 2: Implement the Provider interface

```go
package openai

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"

    "github.com/wzhongyou/llmgate/core"
)

const defaultBaseURL = "https://api.openai.com/v1"

func init() {
    core.RegisterProvider("openai", func(cfg core.ProviderConfig) (core.Provider, error) {
        baseURL := cfg.BaseURL
        if baseURL == "" {
            baseURL = defaultBaseURL
        }
        defaultModel := cfg.DefaultModel
        if defaultModel == "" {
            defaultModel = "gpt-5.5"
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

func (p *Provider) Name() string { return "openai" }

func (p *Provider) Models() []string {
    return []string{"gpt-5.5", "gpt-5.5-instant"}
}

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
    // 1. Convert core.ChatRequest to provider-specific format
    // 2. Make HTTP request
    // 3. Parse response into core.ChatResponse
    // Return &core.ProviderError{...} for all errors (see Step 3)
    return nil, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
    // Same as Chat but with "stream": true and delegates to core.OpenAIStream
    // For custom formats (Anthropic, Gemini) parse SSE manually
    return nil, nil
}
```

---

## Step 3: Error handling

Always return `*core.ProviderError` — never `fmt.Errorf`. Set `Retryable: true` for network errors, 5xx, and 429:

```go
// Network error (retryable)
resp, err := p.client.Do(httpReq)
if err != nil {
    return nil, &core.ProviderError{Provider: "openai", Message: err.Error(), Retryable: true, Cause: err}
}

// HTTP error
if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    return nil, &core.ProviderError{
        Provider:   "openai",
        StatusCode: resp.StatusCode,
        Message:    string(body),
        Retryable:  resp.StatusCode >= 500 || resp.StatusCode == 429,
    }
}

// Parse error (not retryable)
if err := json.Unmarshal(body, &result); err != nil {
    return nil, &core.ProviderError{Provider: "openai", Message: err.Error(), Cause: err}
}
```

The engine uses `Retryable` to decide whether to retry before falling back to the next provider.

---

## Step 4: Conversion patterns

### OpenAI-compatible API (sync + stream)

For providers that follow the OpenAI API format:

```go
// Chat: set "stream": false, call /chat/completions, parse choices[0].message.content
// ChatStream: set "stream": true, keep body open, call core.OpenAIStream(ctx, resp.Body)

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
    // ... build payload with "stream": true ...
    resp, err := p.client.Do(httpReq)
    if err != nil {
        return nil, &core.ProviderError{Provider: "openai", Message: err.Error(), Retryable: true, Cause: err}
    }
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        resp.Body.Close()
        return nil, &core.ProviderError{Provider: "openai", StatusCode: resp.StatusCode, Message: string(body), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
    }
    return core.OpenAIStream(ctx, resp.Body), nil
}
```

OpenAI-compatible response format:
```go
type chatResp struct {
    Choices []struct {
        Message struct {
            Content string `json:"content"`
        } `json:"message"`
    } `json:"choices"`
    Model string `json:"model"`
    Usage struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
        CompletionDetails *struct {
            ReasoningTokens int `json:"reasoning_tokens"`
        } `json:"completion_tokens_details"`
    } `json:"usage"`
}
```

### Non-OpenAI API

For providers with their own API format (Anthropic, Gemini), parse SSE manually in a goroutine. See `core/providers/anthropic/anthropic.go` and `core/providers/gemini/gemini.go` as reference.

---

## Step 5: Token mapping

Map provider-specific usage fields into the unified `core.Usage` struct:

```go
core.Usage{
    InputTokens:     result.Usage.PromptTokens,
    OutputTokens:    result.Usage.CompletionTokens,
    ReasoningTokens: reasoningTokens,
    TotalTokens:     result.Usage.TotalTokens,
}
```

---

## Step 6: Registration

Consumers import your provider with a blank import:

```go
import _ "github.com/wzhongyou/llmgate/core/providers/openai"
```

Also add the provider to the env-var map in `sdk/gateway.go` `loadEnv()`:

```go
"OPENAI_KEY": "openai",
```

---

## Checklist

- [ ] `Name()` returns a unique, lowercase identifier
- [ ] `Models()` lists all supported models
- [ ] `Chat()` handles `req.System` → system prompt
- [ ] `Chat()` uses `req.Model` if set, falls back to `DefaultModel` or built-in default
- [ ] `Chat()` respects `req.MaxTokens`, `req.Temperature`
- [ ] `Chat()` uses `baseURL` from config if set, falls back to built-in default
- [ ] `Chat()` parses reasoning tokens when available
- [ ] `Chat()` passes context to HTTP request
- [ ] `Chat()` returns `*core.ProviderError` for all errors (with correct `Retryable` flag)
- [ ] `ChatStream()` implemented — OpenAI-compatible providers call `core.OpenAIStream(ctx, resp.Body)`
- [ ] `ChatStream()` also returns `*core.ProviderError` before the channel is opened
- [ ] `init()` registers with `core.RegisterProvider`
- [ ] Provider added to `loadEnv()` map in `sdk/gateway.go`

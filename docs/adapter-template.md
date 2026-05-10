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
    "context"
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
    // 1. Convert core.ChatRequest to provider-specific API format
    // 2. Make HTTP request
    // 3. Parse response into core.ChatResponse
    return nil, nil
}
```

---

## Step 3: Conversion patterns

### OpenAI-compatible API

For providers that follow the OpenAI API format (DeepSeek, Groq, Mistral, Qwen, etc.):

```go
// Request format
type chatReq struct {
    Model    string    `json:"model"`
    Messages []message `json:"messages"`
    Stream   bool      `json:"stream"`
}

type message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// Response format
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

For providers with their own API format (Anthropic, Google, etc.), adapt `ChatRequest` to their format accordingly.

---

## Step 4: Token mapping

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

## Step 5: Registration

Consumers import your provider with a blank import:

```go
import _ "github.com/wzhongyou/llmgate/core/providers/openai"
```

Also add the provider to the env-var map in `sdk/gateway.go`:

```go
"OPENAI_KEY": "openai",
```

This triggers the `init()` function which registers the provider. Users can then:

```go
gw.Use("openai", os.Getenv("OPENAI_KEY"))
```

---

## Checklist

- [ ] `Name()` returns a unique, lowercase identifier
- [ ] `Models()` lists all supported models
- [ ] `Chat()` handles `req.System` → system prompt
- [ ] `Chat()` uses `req.Model` if set, falls back to config `DefaultModel` or built-in default
- [ ] `Chat()` respects `req.MaxTokens`, `req.Temperature`
- [ ] `Chat()` uses `baseURL` from config if set, falls back to built-in default
- [ ] `Chat()` parses reasoning tokens when available
- [ ] `Chat()` passes context to HTTP request
- [ ] Error messages include provider name for debugging
- [ ] `init()` registers with `core.RegisterProvider`

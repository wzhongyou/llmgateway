# CLAUDE.md

AI assistant context for this project. Architecture and API are in [docs/design.md](docs/design.md). Adding a provider: [docs/adapter-template.md](docs/adapter-template.md).

---

## Core principle

**Every line of code must earn its place.** No defensive abstractions, no "might need later" helpers, no compatibility shims. If something can be deleted without breaking real use cases, delete it.

---

## Layer rules

| Layer | Logging | Error handling |
|-------|---------|----------------|
| `core/` | Zero — no `log` calls ever | Return typed errors with provider prefix |
| `sdk/` | Zero | Return errors |
| `gateway/` | `slog` structured logging only | Log at middleware boundary, not in handlers |

Violations of the zero-logging rule in `core/` or `sdk/` are bugs, not style issues.

---

## What NOT to do

- Do not add a `testutil` package or any shared test helper package — tests use `llmgateway.New()` + `gw.Engine().GetProvider()` directly
- Do not hardcode provider names in tests — use `providers[0]` or check with `GetProvider()`
- Do not add `init()` auto-imports anywhere except inside each provider's own package
- Do not add comments that explain what the code does — only add one if the WHY is non-obvious
- Do not create `_test.go` helpers that duplicate SDK public API
- Do not add cost/pricing tracking — prices fluctuate and are unreliable to maintain in an SDK

---

## Testing convention

Integration tests skip automatically — no key, no test, no fail:

```go
gw := llmgateway.New()  // auto-loads llmgateway.toml or env vars
if _, ok := gw.Engine().GetProvider("glm"); !ok {
    t.Skip("glm not configured")
}
```

Config for tests: copy `llmgateway.toml.example` → `llmgateway.toml` and fill in keys, or set env vars. No separate test config file.

---

## Config loading

`Validate()` skips providers with empty keys (env var not set). At least one provider must have a key. `InitFromConfig()` also skips empty-key providers when creating them.

---

## Adding a provider

Reference implementation for OpenAI-compatible APIs: [core/providers/glm/glm.go](core/providers/glm/glm.go).  
Reference for custom API format: [core/providers/anthropic/anthropic.go](core/providers/anthropic/anthropic.go) or [core/providers/gemini/gemini.go](core/providers/gemini/gemini.go).  
Full checklist: [docs/adapter-template.md](docs/adapter-template.md).

Register in `sdk/gateway.go` `autoLoad()` env var map and add to both example files.

---

## Access log fields

The gateway middleware logs one line per request. For `/v1/chat`, the log includes LLM-specific fields populated via a `*reqMeta` pointer stored in request context — handlers write to it, middleware reads it at the end. Do not move this logging into handlers or split it across multiple log calls.

Fields logged: `request_id`, `method`, `path`, `status`, `latency_ms`, `remote_addr`, `provider`, `model`, `input_tokens`, `output_tokens`, `reasoning_tokens`.

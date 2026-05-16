package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/wzhongyou/llmgate/core"
)

// Provider is a generic OpenAI-compatible LLM provider.
type Provider struct {
	name         string
	key          string
	baseURL      string
	defaultModel string
	models       []string
	client       http.Client

	// BodyHook mutates the request body map before JSON marshaling.
	// Use for provider-specific adaptations like removing unsupported params.
	BodyHook func(body map[string]interface{})
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Models() []string { return p.models }

func (p *Provider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	body := core.OpenAIBody(model, false, req)
	if p.BodyHook != nil {
		p.BodyHook(body)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &core.ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: string(respBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	result, err := core.OpenAIParseChat(respBody, p.name)
	if err != nil {
		return nil, err
	}
	// Extract <think>...</think> from content for providers that use XML-style reasoning.
	if result.ReasoningContent == "" && strings.Contains(result.Content, "<think>") {
		result.ReasoningContent, result.Content = extractThink(result.Content)
	}
	return result, nil
}

func (p *Provider) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	body := core.OpenAIBody(model, true, req)
	if p.BodyHook != nil {
		p.BodyHook(body)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Cause: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.key)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &core.ProviderError{Provider: p.name, Message: err.Error(), Retryable: true, Cause: err}
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &core.ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: string(errBody), Retryable: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	raw := core.OpenAIStream(ctx, resp.Body)
	return wrapThinkStream(ctx, raw), nil
}

// extractThink pulls <think>...</think> from content.
// Returns the reasoning text and the remaining content with the tag stripped.
func extractThink(content string) (reasoning string, rest string) {
	const open = "<think>"
	if idx := strings.Index(content, open); idx != -1 {
		start := idx + len(open)
		if end := strings.Index(content, "</think>"); end != -1 && end > start {
			reasoning = content[start:end]
			rest = strings.TrimSpace(content[:idx] + content[end+len("</think>"):])
			return reasoning, rest
		}
	}
	return "", content
}

// wrapThinkStream wraps a StreamChunk channel to extract <think>...</think> tags
// from Content deltas into ReasoningContent. This handles providers like GLM that
// embed reasoning inline rather than sending a separate reasoning_content delta.
func wrapThinkStream(ctx context.Context, in <-chan core.StreamChunk) <-chan core.StreamChunk {
	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		var buf strings.Builder
		inThink := false
		const maxTag = 8 // len("</think>")

		forward := func(sc core.StreamChunk) {
			select {
			case out <- sc:
			case <-ctx.Done():
			}
		}

		for chunk := range in {
			if chunk.Error != nil {
				out <- chunk
				return
			}

			// Pass through chunks that carry no text delta (usage, tool calls).
			if chunk.Content == "" && chunk.ReasoningContent == "" {
				forward(chunk)
				continue
			}

			buf.WriteString(chunk.Content)
			raw := buf.String()
			buf.Reset()

			// Keep trailing bytes that might start a partial tag.
			safe := raw
			if len(safe) > maxTag {
				safe = raw[:len(raw)-maxTag]
				buf.WriteString(raw[len(raw)-maxTag:])
			} else {
				buf.WriteString(raw)
				continue
			}

			rest := safe
			for rest != "" {
				if !inThink {
					idx := strings.Index(rest, "<think>")
					if idx < 0 {
						forward(core.StreamChunk{Content: rest, Model: chunk.Model})
						break
					}
					if idx > 0 {
						forward(core.StreamChunk{Content: rest[:idx], Model: chunk.Model})
					}
					rest = rest[idx+len("<think>"):]
					inThink = true
				} else {
					idx := strings.Index(rest, "</think>")
					if idx < 0 {
						forward(core.StreamChunk{ReasoningContent: rest, Model: chunk.Model})
						break
					}
					if idx > 0 {
						forward(core.StreamChunk{ReasoningContent: rest[:idx], Model: chunk.Model})
					}
					rest = rest[idx+len("</think>"):]
					inThink = false
				}
			}
		}

		// Flush remaining buffer.
		if buf.Len() > 0 {
			if inThink {
				forward(core.StreamChunk{ReasoningContent: buf.String()})
			} else {
				forward(core.StreamChunk{Content: buf.String()})
			}
		}
	}()
	return out
}

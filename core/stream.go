package core

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// OpenAIStream parses a streaming HTTP response body from any OpenAI-compatible
// provider and emits StreamChunks. The caller must not close body; this function
// owns it and closes it when the stream ends or ctx is cancelled.
func OpenAIStream(ctx context.Context, body io.ReadCloser) <-chan StreamChunk {
	ch := make(chan StreamChunk, 16)
	go func() {
		defer close(ch)
		defer body.Close()

		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			var event struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Model string `json:"model"`
				Usage *struct {
					PromptTokens            int `json:"prompt_tokens"`
					CompletionTokens        int `json:"completion_tokens"`
					TotalTokens             int `json:"total_tokens"`
					CompletionTokensDetails *struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"completion_tokens_details"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				select {
				case ch <- StreamChunk{Error: err}:
				case <-ctx.Done():
				}
				return
			}

			if len(event.Choices) == 0 {
				continue
			}

			sc := StreamChunk{
				Content: event.Choices[0].Delta.Content,
				Model:   event.Model,
			}
			if event.Usage != nil {
				reasoning := 0
				if event.Usage.CompletionTokensDetails != nil {
					reasoning = event.Usage.CompletionTokensDetails.ReasoningTokens
				}
				sc.Usage = &Usage{
					InputTokens:     event.Usage.PromptTokens,
					OutputTokens:    event.Usage.CompletionTokens,
					ReasoningTokens: reasoning,
					TotalTokens:     event.Usage.TotalTokens,
				}
			}

			select {
			case ch <- sc:
			case <-ctx.Done():
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- StreamChunk{Error: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}

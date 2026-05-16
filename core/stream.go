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

		type toolCallDelta struct {
			index    int
			id       string
			callType string
			name     string
			args     strings.Builder
		}
		var toolAccs []*toolCallDelta

		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var event struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
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

			delta := event.Choices[0].Delta

			// Accumulate tool call deltas indexed by tool_calls[].index
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				for len(toolAccs) <= idx {
					toolAccs = append(toolAccs, &toolCallDelta{index: len(toolAccs)})
				}
				acc := toolAccs[idx]
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Type != "" {
					acc.callType = tc.Type
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}

			sc := StreamChunk{
				Content:          delta.Content,
				ReasoningContent: delta.ReasoningContent,
				Model:            event.Model,
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

			if sc.Content != "" || sc.ReasoningContent != "" || sc.Usage != nil {
				select {
				case ch <- sc:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- StreamChunk{Error: err}:
			case <-ctx.Done():
			}
			return
		}

		// Emit accumulated tool calls as final chunk
		if len(toolAccs) > 0 {
			calls := make([]ToolCall, len(toolAccs))
			for i, acc := range toolAccs {
				callType := acc.callType
				if callType == "" {
					callType = "function"
				}
				calls[i] = ToolCall{
					ID:   acc.id,
					Type: callType,
					Function: FunctionCall{
						Name:      acc.name,
						Arguments: acc.args.String(),
					},
				}
			}
			select {
			case ch <- StreamChunk{ToolCalls: calls, FinishReason: "tool_calls"}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}

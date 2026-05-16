package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wzhongyou/llmgate/core"
	"github.com/wzhongyou/llmgate/server"

	_ "github.com/wzhongyou/llmgate/core/providers/openaicompat"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	key := os.Getenv("DEEPSEEK_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_KEY not set")
	}
	srv, err := server.New(&server.Config{
		Providers: []core.ProviderConfig{{Name: "deepseek", Key: key}},
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return httptest.NewServer(srv.Handler())
}

func TestServer_Health(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServer_Models(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Data []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) == 0 {
		t.Error("expected non-empty models list")
	}
	t.Logf("Models: %+v", body.Data)
}

func TestServer_Chat(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "你好，请用一句话介绍你自己。"}},
	})
	resp, err := http.Post(ts.URL+"/v1/chat", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, e)
	}

	var chatResp core.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if chatResp.Content == "" {
		t.Error("expected non-empty content")
	}
	if chatResp.Provider != "deepseek" {
		t.Errorf("expected provider=deepseek, got %q", chatResp.Provider)
	}
	if chatResp.Usage.TotalTokens <= 0 {
		t.Errorf("expected TotalTokens > 0, got %d", chatResp.Usage.TotalTokens)
	}
	t.Logf("Model: %s, Tokens: %d (in=%d out=%d reasoning=%d), Latency: %v",
		chatResp.Model, chatResp.Usage.TotalTokens,
		chatResp.Usage.InputTokens, chatResp.Usage.OutputTokens, chatResp.Usage.ReasoningTokens,
		chatResp.Latency)
}

func TestServer_ChatWithProvider(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "1+1=?"}},
	})
	resp, err := http.Post(ts.URL+"/v1/chat?provider=deepseek", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	t.Log("Chat with provider query param OK")
}

func TestServer_Fallback(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "Hello!"}},
	})
	resp, err := http.Post(ts.URL+"/v1/chat?fallback=nonexistent&fallback=deepseek", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, e)
	}

	var chatResp core.ChatResponse
	json.NewDecoder(resp.Body).Decode(&chatResp)
	t.Logf("Fallback response from: %s", chatResp.Provider)
}

func TestServer_ContextCancellation(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(core.ChatRequest{
		Messages:  []core.Message{{Role: "user", Content: "Write a very long essay about the history of computing."}},
		MaxTokens: intPtr(10000),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	_, err = http.DefaultClient.Do(req)
	if err == nil {
		t.Log("Request completed despite short timeout (unexpected but possible)")
	} else {
		t.Logf("Expected timeout/cancellation: %v", err)
	}
}

func TestServer_ChatStream(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(core.ChatRequest{
		Messages:  []core.Message{{Role: "user", Content: "Count to 3."}},
		MaxTokens: intPtr(50),
		Stream:    true,
	})
	resp, err := http.Post(ts.URL+"/v1/chat", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /v1/chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}

	var content string
	var chunks int
	done := false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			done = true
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk struct {
			Content string      `json:"Content"`
			Usage   *core.Usage `json:"Usage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("parse chunk: %v", err)
		}
		content += chunk.Content
		chunks++
	}

	if !done {
		t.Error("expected data: [DONE] terminator")
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
	t.Logf("chunks=%d content=%q", chunks, content)
}

func intPtr(i int) *int { return &i }

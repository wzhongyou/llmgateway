package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/wzhongyou/llmgateway/core"
	"github.com/wzhongyou/llmgateway/gateway"

	_ "github.com/wzhongyou/llmgateway/core/providers/deepseek"
)

func newGatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	key := os.Getenv("DEEPSEEK_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_KEY not set")
	}
	srv, err := gateway.New(&core.GatewayConfig{
		Providers: []core.ProviderConfig{{Name: "deepseek", Key: key}},
	})
	if err != nil {
		t.Fatalf("gateway.New: %v", err)
	}
	return httptest.NewServer(srv.Handler())
}

func TestGateway_Health(t *testing.T) {
	ts := newGatewayServer(t)
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

func TestGateway_Models(t *testing.T) {
	ts := newGatewayServer(t)
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

func TestGateway_Chat(t *testing.T) {
	ts := newGatewayServer(t)
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

func TestGateway_ChatWithProvider(t *testing.T) {
	ts := newGatewayServer(t)
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

func TestGateway_Fallback(t *testing.T) {
	ts := newGatewayServer(t)
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

func TestGateway_ContextCancellation(t *testing.T) {
	ts := newGatewayServer(t)
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

func intPtr(i int) *int { return &i }

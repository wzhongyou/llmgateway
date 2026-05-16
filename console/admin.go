package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wzhongyou/llmgate/core"
)

// Setup registers all admin API routes and static file serving on the mux.
func (c *Console) Setup(mux *http.ServeMux) {
	// Channels
	mux.HandleFunc("GET /admin/api/channels", c.handleChannelsList)
	mux.HandleFunc("GET /admin/api/channels/{name}", c.handleChannelGet)
	mux.HandleFunc("PUT /admin/api/channels/{name}", c.handleChannelPut)
	mux.HandleFunc("DELETE /admin/api/channels/{name}", c.handleChannelDelete)
	mux.HandleFunc("POST /admin/api/channels/{name}/test", c.handleChannelTest)
	// Playground
	mux.HandleFunc("POST /admin/api/playground/chat", c.handlePlaygroundChat)
	mux.HandleFunc("POST /admin/api/playground/stream", c.handlePlaygroundStream)
	// Mock rules
	mux.HandleFunc("GET /admin/api/mock/rules", c.handleMockList)
	mux.HandleFunc("POST /admin/api/mock/rules", c.handleMockCreate)
	mux.HandleFunc("PUT /admin/api/mock/rules/{id}", c.handleMockUpdate)
	mux.HandleFunc("DELETE /admin/api/mock/rules/{id}", c.handleMockDelete)
	mux.HandleFunc("POST /admin/api/mock/rules/reorder", c.handleMockReorder)
	// Recent requests
	mux.HandleFunc("GET /admin/api/recent", c.handleRecentList)
	mux.HandleFunc("GET /admin/api/recent/{id}", c.handleRecentDetail)
	// Config
	mux.HandleFunc("POST /admin/api/config/save", c.handleConfigSave)
	// Static files
	mux.HandleFunc("GET /admin/", c.handleStatic)
	mux.HandleFunc("GET /admin", c.handleStatic)
}

// --- channel handlers ---

type channelInfo struct {
	Name         string   `json:"name"`
	Models       []string `json:"models"`
	TotalCalls   int64    `json:"total_calls"`
	ErrorCalls   int64    `json:"error_calls"`
	AvgLatencyMs float64  `json:"avg_latency_ms"`
	Available    bool     `json:"available"`
	KeyRef       string   `json:"key_ref"` // original TOML key value (masked)
}

func (c *Console) handleChannelsList(w http.ResponseWriter, r *http.Request) {
	snap := c.engine.Snapshot()
	providers := c.engine.Providers()

	channels := make([]channelInfo, 0, len(providers))
	for _, p := range providers {
		ci := channelInfo{
			Name:   p.Name(),
			Models: safeModels(p.Models()),
		}
		if stat, ok := snap.Providers[p.Name()]; ok {
			ci.TotalCalls = stat.TotalCalls
			ci.ErrorCalls = stat.ErrorCalls
			ci.AvgLatencyMs = stat.AvgLatencyMs
			ci.Available = stat.Available
		}
		if ref, ok := c.rawProviderKeys[p.Name()]; ok {
			ci.KeyRef = maskKey(ref)
		}
		channels = append(channels, ci)
	}
	writeJSON(w, http.StatusOK, channels)
}

func (c *Console) handleChannelGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := c.engine.GetProvider(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	ci := channelInfo{
		Name:   p.Name(),
		Models: safeModels(p.Models()),
	}
	snap := c.engine.Snapshot()
	if stat, ok := snap.Providers[name]; ok {
		ci.TotalCalls = stat.TotalCalls
		ci.ErrorCalls = stat.ErrorCalls
		ci.AvgLatencyMs = stat.AvgLatencyMs
		ci.Available = stat.Available
	}
	if ref, ok := c.rawProviderKeys[name]; ok {
		ci.KeyRef = maskKey(ref)
	}
	writeJSON(w, http.StatusOK, ci)
}

func (c *Console) handleChannelPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var cfg core.ProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg.Name = name

	// Store raw key ref for config persistence.
	if c.rawProviderKeys == nil {
		c.rawProviderKeys = make(map[string]string)
	}
	if cfg.Key != "" {
		c.rawProviderKeys[name] = cfg.Key
	} else if ref, ok := c.rawProviderKeys[name]; ok {
		cfg.Key = ref
	}

	// Expand ${VAR} before passing to the engine.
	cfg.Key = core.ExpandEnv(cfg.Key)

	p, err := c.engine.CreateProvider(cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Register replaces any existing provider with the same name.
	c.engine.Register(p)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *Console) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Engine doesn't have unregister — we just note it as removed.
	// The provider stays in the engine but won't be in the config after save.
	delete(c.rawProviderKeys, name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *Console) handleChannelTest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req := &core.ChatRequest{
		Model:    "ping",
		Messages: []core.Message{{Role: "user", Content: "ping"}},
		MaxTokens: intPtr(1),
	}
	resp, err := c.engine.ChatWithProvider(ctx, req, name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"model":      resp.Model,
		"latency_ms": float64(resp.Latency.Microseconds()) / 1000.0,
	})
}

// --- playground handlers ---

func (c *Console) handlePlaygroundChat(w http.ResponseWriter, r *http.Request) {
	var req core.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	provider := r.URL.Query().Get("provider")
	req.Stream = false

	start := time.Now()
	var resp *core.ChatResponse
	var err error
	if provider != "" {
		resp, err = c.engine.ChatWithProvider(r.Context(), &req, provider)
	} else {
		resp, err = c.engine.Chat(r.Context(), &req)
	}
	latency := time.Since(start)

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"content":       resp.Content,
		"tool_calls":    resp.ToolCalls,
		"finish_reason": resp.FinishReason,
		"model":         resp.Model,
		"provider":      resp.Provider,
		"usage":         resp.Usage,
		"latency_ms":    float64(latency.Microseconds()) / 1000.0,
	})
}

func (c *Console) handlePlaygroundStream(w http.ResponseWriter, r *http.Request) {
	var req core.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	provider := r.URL.Query().Get("provider")
	req.Stream = true

	var ch <-chan core.StreamChunk
	var err error
	if provider != "" {
		ch, err = c.engine.ChatStreamWithProvider(r.Context(), &req, provider)
	} else {
		ch, err = c.engine.ChatStream(r.Context(), &req)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	for chunk := range ch {
		if chunk.Error != nil {
			fmt.Fprintf(w, "data: {\"error\":%q}\n\n", chunk.Error.Error())
			flusher.Flush()
			return
		}
		fmt.Fprint(w, "data: ")
		enc.Encode(chunk)
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// --- mock rule handlers ---

func (c *Console) handleMockList(w http.ResponseWriter, r *http.Request) {
	rules := c.mockStore.Rules()
	if rules == nil {
		rules = []MockRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (c *Console) handleMockCreate(w http.ResponseWriter, r *http.Request) {
	var rule MockRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rule.ID = newID()
	rules := c.mockStore.Rules()
	// Assign next priority
	if len(rules) > 0 {
		max := 0
		for _, r := range rules {
			if r.Priority > max {
				max = r.Priority
			}
		}
		if rule.Priority == 0 {
			rule.Priority = max + 1
		}
	}
	rules = append(rules, rule)
	c.mockStore.Set(rules)
	writeJSON(w, http.StatusCreated, rule)
}

func (c *Console) handleMockUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updated MockRule
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rules := c.mockStore.Rules()
	found := false
	for i := range rules {
		if rules[i].ID == id {
			updated.ID = id
			rules[i] = updated
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}
	c.mockStore.Set(rules)
	writeJSON(w, http.StatusOK, updated)
}

func (c *Console) handleMockDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rules := c.mockStore.Rules()
	found := false
	for i := range rules {
		if rules[i].ID == id {
			rules = append(rules[:i], rules[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}
	c.mockStore.Set(rules)
	writeJSON(w, http.StatusNoContent, nil)
}

func (c *Console) handleMockReorder(w http.ResponseWriter, r *http.Request) {
	var order []struct {
		ID       string `json:"id"`
		Priority int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rules := c.mockStore.Rules()
	for i := range rules {
		for _, o := range order {
			if rules[i].ID == o.ID {
				rules[i].Priority = o.Priority
				break
			}
		}
	}
	c.mockStore.Set(rules)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- recent request handlers ---

func (c *Console) handleRecentList(w http.ResponseWriter, r *http.Request) {
	entries := c.recentReqs.List(50)
	if entries == nil {
		entries = []RecentEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (c *Console) handleRecentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, ok := c.recentReqs.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// --- config handlers ---

func (c *Console) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	if c.saveConfig == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save not supported — no config path"})
		return
	}
	if err := c.saveConfig(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- static file serving ---

func (c *Console) handleStatic(w http.ResponseWriter, r *http.Request) {
	// Map /admin/<file> to embedded static/<file>
	sub := r.URL.Path[len("/admin"):]
	if sub == "" || sub == "/" {
		sub = "/index.html"
	}
	filePath := "static" + sub
	data, err := staticFiles.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	// Sniff Content-Type from extension.
	switch {
	case len(filePath) > 5 && filePath[len(filePath)-5:] == ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case len(filePath) > 3 && filePath[len(filePath)-3:] == ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case len(filePath) > 4 && filePath[len(filePath)-4:] == ".css":
		w.Header().Set("Content-Type", "text/css")
	}
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// --- helpers ---

func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 2 && key[0] == '$' && key[1] == '{' {
		return key // show env var references as-is
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func intPtr(i int) *int { return &i }

func safeModels(m []string) []string {
	if m == nil {
		return []string{}
	}
	return m
}

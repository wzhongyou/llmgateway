package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wzhongyou/llmgate/core"
	"github.com/wzhongyou/llmgate/sdk"
)

type Server struct {
	mu     sync.RWMutex
	gw     *sdk.Gateway
	cfg    *Config
	logger *slog.Logger
	rl     *rateLimiter
}

type Option func(*Server)

func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.logger = l }
}

func New(cfg *Config, opts ...Option) (*Server, error) {
	gw := sdk.New()
	if err := gw.InitFromConfig(cfg.coreConfig()); err != nil {
		return nil, err
	}
	s := &Server{
		gw:     gw,
		cfg:    cfg,
		logger: slog.Default(),
	}
	if cfg.Server.RateLimitRPM > 0 {
		s.rl = newRateLimiter(cfg.Server.RateLimitRPM)
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *Server) Gateway() *sdk.Gateway {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gw
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", s.handleChat)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/health/live", s.handleLive)
	mux.HandleFunc("/health/ready", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
	return s.middleware(mux)
}

// WatchConfig polls cfgPath for changes and reloads providers when the file is modified.
func (s *Server) WatchConfig(ctx context.Context, cfgPath string) {
	go func() {
		info, err := os.Stat(cfgPath)
		if err != nil {
			s.logger.Error("config watch: stat failed", "path", cfgPath, "error", err)
			return
		}
		lastMod := info.ModTime()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(cfgPath)
				if err != nil || !info.ModTime().After(lastMod) {
					continue
				}
				lastMod = info.ModTime()
				newCfg, err := LoadConfig(cfgPath)
				if err != nil {
					s.logger.Error("config reload failed", "error", err)
					continue
				}
				if err := s.reload(newCfg); err != nil {
					s.logger.Error("config apply failed", "error", err)
					continue
				}
				s.logger.Info("config reloaded", "path", cfgPath)
			}
		}
	}()
}

func (s *Server) reload(cfg *Config) error {
	newGW := sdk.New()
	if err := newGW.InitFromConfig(cfg.coreConfig()); err != nil {
		return err
	}
	s.mu.Lock()
	s.gw = newGW
	s.cfg = cfg
	if cfg.Server.RateLimitRPM > 0 {
		s.rl = newRateLimiter(cfg.Server.RateLimitRPM)
	} else {
		s.rl = nil
	}
	s.mu.Unlock()
	return nil
}

// statusWriter wraps ResponseWriter to capture the HTTP status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// reqMeta holds LLM-specific fields populated by the chat handler
// and flushed into the access log by the middleware.
type reqMeta struct {
	provider        string
	model           string
	inputTokens     int
	outputTokens    int
	reasoningTokens int
	errMsg          string
}

type ctxKey struct{}

func newRequestID() string {
	var b [16]byte
	rand.Read(b[:]) //nolint:errcheck
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API key auth
		s.mu.RLock()
		apiKeys := s.cfg.Server.APIKeys
		rl := s.rl
		s.mu.RUnlock()

		if len(apiKeys) > 0 {
			token := r.Header.Get("Authorization")
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
			allowed := false
			for _, k := range apiKeys {
				if k == token {
					allowed = true
					break
				}
			}
			if !allowed {
				writeJSONDirect(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}

		// Rate limiting
		if rl != nil && !rl.allow() {
			writeJSONDirect(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}

		start := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-Id", requestID)

		meta := &reqMeta{}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, meta))

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

		attrs := []slog.Attr{
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Float64("latency_ms", latencyMs),
			slog.String("remote_addr", r.RemoteAddr),
		}
		// LLM-specific fields — only present on /v1/chat
		if meta.provider != "" {
			attrs = append(attrs,
				slog.String("provider", meta.provider),
				slog.String("model", meta.model),
				slog.Int("input_tokens", meta.inputTokens),
				slog.Int("output_tokens", meta.outputTokens),
				slog.Int("reasoning_tokens", meta.reasoningTokens),
			)
		}
		if meta.errMsg != "" {
			attrs = append(attrs, slog.String("error", meta.errMsg))
		}

		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelError
		} else if sw.status >= 400 {
			level = slog.LevelWarn
		}
		s.logger.LogAttrs(r.Context(), level, "request", attrs...)
	})
}

// handleLive is the liveness probe — always 200 while the process is running.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is the readiness probe — 200 if at least one provider is available.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	gw := s.gw
	s.mu.RUnlock()

	snap := gw.Snapshot()
	for _, stat := range snap.Providers {
		if stat.Available {
			s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
	}
	// No stats yet means no calls made — treat as ready if providers are configured.
	if len(snap.Providers) == 0 && len(gw.ProviderNames()) > 0 {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
}

// handleHealth keeps backward compatibility — alias for liveness.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.handleLive(w, r)
}

// handleMetrics returns provider metrics in Prometheus text exposition format.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	gw := s.gw
	s.mu.RUnlock()

	snap := gw.Snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)

	fmt.Fprint(w, "# HELP llmgate_requests_total Total requests per provider\n")
	fmt.Fprint(w, "# TYPE llmgate_requests_total counter\n")
	for name, stat := range snap.Providers {
		fmt.Fprintf(w, "llmgate_requests_total{provider=%q} %d\n", name, stat.TotalCalls)
	}

	fmt.Fprint(w, "# HELP llmgate_errors_total Total errors per provider\n")
	fmt.Fprint(w, "# TYPE llmgate_errors_total counter\n")
	for name, stat := range snap.Providers {
		fmt.Fprintf(w, "llmgate_errors_total{provider=%q} %d\n", name, stat.ErrorCalls)
	}

	fmt.Fprint(w, "# HELP llmgate_provider_avg_latency_ms Average latency in milliseconds per provider\n")
	fmt.Fprint(w, "# TYPE llmgate_provider_avg_latency_ms gauge\n")
	for name, stat := range snap.Providers {
		fmt.Fprintf(w, "llmgate_provider_avg_latency_ms{provider=%q} %.3f\n", name, stat.AvgLatencyMs)
	}

	fmt.Fprint(w, "# HELP llmgate_provider_available Provider availability (1=available, 0=unavailable)\n")
	fmt.Fprint(w, "# TYPE llmgate_provider_available gauge\n")
	for name, stat := range snap.Providers {
		avail := 0
		if stat.Available {
			avail = 1
		}
		fmt.Fprintf(w, "llmgate_provider_available{provider=%q} %d\n", name, avail)
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	type modelInfo struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	s.mu.RLock()
	gw := s.gw
	s.mu.RUnlock()

	models := make([]modelInfo, 0)
	for _, p := range gw.Engine().Providers() {
		for _, m := range p.Models() {
			models = append(models, modelInfo{ID: m, Provider: p.Name()})
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": models})
}
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req core.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	provider := r.URL.Query().Get("provider")
	fallback := r.URL.Query()["fallback"]

	if req.Stream {
		s.handleChatStream(w, r, &req, provider, fallback)
		return
	}

	s.mu.RLock()
	gw := s.gw
	s.mu.RUnlock()

	var resp *core.ChatResponse
	var err error

	switch {
	case len(fallback) > 0:
		resp, err = gw.Fallback(fallback...).Chat(r.Context(), &req)
	case provider != "":
		resp, err = gw.With(provider).Chat(r.Context(), &req)
	default:
		resp, err = gw.Chat(r.Context(), &req)
	}

	if meta, ok := r.Context().Value(ctxKey{}).(*reqMeta); ok {
		if err != nil {
			meta.errMsg = err.Error()
		} else {
			meta.provider = resp.Provider
			meta.model = resp.Model
			meta.inputTokens = resp.Usage.InputTokens
			meta.outputTokens = resp.Usage.OutputTokens
			meta.reasoningTokens = resp.Usage.ReasoningTokens
		}
	}

	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request, req *core.ChatRequest, provider string, fallback []string) {
	s.mu.RLock()
	gw := s.gw
	s.mu.RUnlock()

	var ch <-chan core.StreamChunk
	var err error

	switch {
	case provider != "":
		ch, err = gw.With(provider).ChatStream(r.Context(), req)
	default:
		ch, err = gw.ChatStream(r.Context(), req)
	}

	meta, hasMeta := r.Context().Value(ctxKey{}).(*reqMeta)

	if err != nil {
		if hasMeta {
			meta.errMsg = err.Error()
		}
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	for chunk := range ch {
		if chunk.Error != nil {
			if hasMeta {
				meta.errMsg = chunk.Error.Error()
			}
			fmt.Fprintf(w, "data: {\"error\":%q}\n\n", chunk.Error.Error())
			flusher.Flush()
			return
		}
		if chunk.Usage != nil && hasMeta {
			meta.inputTokens = chunk.Usage.InputTokens
			meta.outputTokens = chunk.Usage.OutputTokens
			meta.reasoningTokens = chunk.Usage.ReasoningTokens
		}
		if chunk.Model != "" && hasMeta && meta.model == "" {
			meta.model = chunk.Model
		}
		fmt.Fprint(w, "data: ")
		enc.Encode(chunk)
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("write error", "error", err)
	}
}

// writeJSONDirect is used before the logger is available (e.g., in auth middleware).
func writeJSONDirect(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (s *Server) ListenAndServe(addr string) error {
	addr = s.resolveAddr(addr)
	svr := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	s.logger.Info("gateway: listening", "addr", addr)
	return svr.ListenAndServe()
}

func (s *Server) ListenAndServeWithContext(ctx context.Context, addr string) error {
	addr = s.resolveAddr(addr)
	svr := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("gateway: listening", "addr", addr)
		errCh <- svr.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.logger.Info("gateway: shutting down")
		return svr.Shutdown(shutdownCtx)
	}
}

func (s *Server) resolveAddr(addr string) string {
	if addr != "" {
		return addr
	}
	s.mu.RLock()
	listenAddr := s.cfg.Server.ListenAddr
	s.mu.RUnlock()
	if listenAddr != "" {
		return listenAddr
	}
	return ":8080"
}

// rateLimiter is a simple token bucket for global RPM limiting.
type rateLimiter struct {
	mu         sync.Mutex
	tokens     int64
	maxTokens  int64
	refillRate float64 // tokens per nanosecond
	lastRefill time.Time
}

func newRateLimiter(rpm int) *rateLimiter {
	return &rateLimiter{
		tokens:     int64(rpm),
		maxTokens:  int64(rpm),
		refillRate: float64(rpm) / float64(time.Minute),
		lastRefill: time.Now(),
	}
}

func (r *rateLimiter) allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(r.lastRefill)
	refill := int64(float64(elapsed) * r.refillRate)
	if refill > 0 {
		r.tokens += refill
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.lastRefill = now
	}
	if r.tokens <= 0 {
		return false
	}
	r.tokens--
	return true
}

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wzhongyou/llmgateway/core"
	"github.com/wzhongyou/llmgateway/sdk"
)

type Server struct {
	gw     *sdk.Gateway
	cfg    *core.GatewayConfig
	logger *slog.Logger
}

type Option func(*Server)

func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.logger = l }
}

func New(cfg *core.GatewayConfig, opts ...Option) (*Server, error) {
	gw := sdk.New()
	if err := gw.InitFromConfig(cfg); err != nil {
		return nil, err
	}
	s := &Server{
		gw:     gw,
		cfg:    cfg,
		logger: slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *Server) Gateway() *sdk.Gateway { return s.gw }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", s.handleChat)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", s.handleHealth)
	return s.middleware(mux)
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

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := fmt.Sprintf("%d", start.UnixNano())
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	type modelInfo struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	models := make([]modelInfo, 0)
	for _, p := range s.gw.Engine().Providers() {
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

	var resp *core.ChatResponse
	var err error

	switch {
	case len(fallback) > 0:
		resp, err = s.gw.Fallback(fallback...).Chat(r.Context(), &req)
	case provider != "":
		resp, err = s.gw.With(provider).Chat(r.Context(), &req)
	default:
		resp, err = s.gw.Chat(r.Context(), &req)
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

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("write error", "error", err)
	}
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
	if s.cfg.Server.ListenAddr != "" {
		return s.cfg.Server.ListenAddr
	}
	return ":8080"
}

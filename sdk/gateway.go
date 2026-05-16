package sdk

import (
	"context"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/wzhongyou/llmgate/core"
)

type Gateway struct {
	engine   *core.Engine
	pinnedTo string
	fallback []string
}

// New creates a Gateway and auto-loads providers from environment variables.
func New() *Gateway {
	g := &Gateway{engine: core.NewEngine(nil)}
	g.loadEnv()
	return g
}

// NewFromFile creates a Gateway from an explicit config file path.
func NewFromFile(path string) (*Gateway, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sdk: read config: %w", err)
	}
	var cfg core.GatewayConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("sdk: parse config: %w", err)
	}
	cfg.ApplyEnv()
	g := &Gateway{engine: core.NewEngine(nil)}
	if err := g.InitFromConfig(&cfg); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *Gateway) loadEnv() {
	envProviders := map[string]string{
		"ANTHROPIC_KEY": "anthropic",
		"DEEPSEEK_KEY":  "deepseek",
		"ERNIE_KEY":     "ernie",
		"GEMINI_KEY":    "gemini",
		"GLM_KEY":       "glm",
		"GROK_KEY":      "grok",
		"HUNYUAN_KEY":   "hunyuan",
		"KIMI_KEY":      "kimi",
		"LLAMA_KEY":     "llama",
		"MIMO_KEY":      "mimo",
		"MINIMAX_KEY":   "minimax",
		"OPENAI_KEY":    "openai",
		"QWEN_KEY":      "qwen",
		"STEPFUN_KEY":   "stepfun",
	}
	for env, name := range envProviders {
		if key := os.Getenv(env); key != "" {
			cfg := core.ProviderConfig{Name: name, Key: key}
			if p, err := g.engine.CreateProvider(cfg); err == nil {
				g.engine.Register(p)
			}
		}
	}
}

func (g *Gateway) Use(name, key string) error {
	cfg := core.ProviderConfig{Name: name, Key: key}
	p, err := g.engine.CreateProvider(cfg)
	if err != nil {
		return err
	}
	g.engine.Register(p)
	return nil
}

func (g *Gateway) UseWithConfig(cfg core.ProviderConfig) error {
	p, err := g.engine.CreateProvider(cfg)
	if err != nil {
		return err
	}
	g.engine.Register(p)
	return nil
}

func (g *Gateway) UseStrategy(s core.Strategy) {
	g.engine.SetStrategy(s)
}

func (g *Gateway) With(name string) *Gateway {
	return &Gateway{
		engine:   g.engine,
		pinnedTo: name,
	}
}

func (g *Gateway) Fallback(names ...string) *Gateway {
	return &Gateway{
		engine:   g.engine,
		fallback: names,
	}
}

func (g *Gateway) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	switch {
	case len(g.fallback) > 0:
		return g.engine.ChatWithFallback(ctx, req, g.fallback)
	case g.pinnedTo != "":
		return g.engine.ChatWithProvider(ctx, req, g.pinnedTo)
	default:
		return g.engine.Chat(ctx, req)
	}
}

func (g *Gateway) ChatStream(ctx context.Context, req *core.ChatRequest) (<-chan core.StreamChunk, error) {
	switch {
	case g.pinnedTo != "":
		return g.engine.ChatStreamWithProvider(ctx, req, g.pinnedTo)
	default:
		return g.engine.ChatStream(ctx, req)
	}
}

func (g *Gateway) Snapshot() core.MetricsSnapshot {
	return g.engine.Snapshot()
}

func (g *Gateway) ProviderNames() []string {
	ps := g.engine.Providers()
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name()
	}
	return names
}

func (g *Gateway) Models() []string {
	var models []string
	for _, p := range g.engine.Providers() {
		models = append(models, p.Models()...)
	}
	return models
}

func (g *Gateway) Engine() *core.Engine {
	return g.engine
}

func (g *Gateway) InitFromConfig(cfg *core.GatewayConfig) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("sdk: %w", err)
	}

	strategy := buildStrategy(&cfg.Strategy)

	for _, pc := range cfg.Providers {
		if pc.Key == "" {
			continue
		}
		pcfg := core.ProviderConfig{
			Name:         pc.Name,
			Key:          pc.Key,
			BaseURL:      pc.BaseURL,
			DefaultModel: pc.DefaultModel,
		}
		p, err := g.engine.CreateProvider(pcfg)
		if err != nil {
			return fmt.Errorf("sdk: %w", err)
		}
		g.engine.Register(p)
	}

	if strategy != nil {
		g.engine.SetStrategy(strategy)
	}
	return nil
}

func buildStrategy(sc *core.StrategyConfig) core.Strategy {
	if sc.Primary == "" {
		return nil
	}
	inner := &core.PrimaryFirstStrategy{
		Primary:  sc.Primary,
		Fallback: sc.Fallback,
	}

	var wrapped core.Strategy = inner
	if sc.LatencyThresholdMs > 0 {
		wrapped = core.NewLatencyStrategy(wrapped, float64(sc.LatencyThresholdMs))
	}
	return wrapped
}

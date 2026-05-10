package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"

	"github.com/wzhongyou/llmgateway/gateway"

	// Register built-in providers
	_ "github.com/wzhongyou/llmgateway/core/providers/anthropic"
	_ "github.com/wzhongyou/llmgateway/core/providers/deepseek"
	_ "github.com/wzhongyou/llmgateway/core/providers/ernie"
	_ "github.com/wzhongyou/llmgateway/core/providers/gemini"
	_ "github.com/wzhongyou/llmgateway/core/providers/glm"
	_ "github.com/wzhongyou/llmgateway/core/providers/grok"
	_ "github.com/wzhongyou/llmgateway/core/providers/hunyuan"
	_ "github.com/wzhongyou/llmgateway/core/providers/kimi"
	_ "github.com/wzhongyou/llmgateway/core/providers/llama"
	_ "github.com/wzhongyou/llmgateway/core/providers/mimo"
	_ "github.com/wzhongyou/llmgateway/core/providers/minimax"
	_ "github.com/wzhongyou/llmgateway/core/providers/openai"
	_ "github.com/wzhongyou/llmgateway/core/providers/qwen"
	_ "github.com/wzhongyou/llmgateway/core/providers/stepfun"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Default: reads llmgateway.toml from project root.
	// Pass a path as first argument to override, e.g. go run main.go gateway.toml
	cfgPath := "llmgateway.toml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := gateway.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	server, err := gateway.New(cfg, gateway.WithLogger(logger))
	if err != nil {
		log.Fatalf("server error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := server.ListenAndServeWithContext(ctx, ""); err != nil {
		log.Fatalf("serve error: %v", err)
	}
}


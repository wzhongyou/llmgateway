package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"

	"github.com/wzhongyou/llmgate/server"
)

// go run ./examples/server
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Default: reads llmgate.toml from project root.
	// Pass a path as first argument to override, e.g. go run main.go /path/to/llmgate.toml
	cfgPath := "llmgate.toml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := server.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	srv, err := server.New(cfg, server.WithLogger(logger))
	if err != nil {
		log.Fatalf("server error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := srv.ListenAndServeWithContext(ctx, ""); err != nil {
		log.Fatalf("serve error: %v", err)
	}
}

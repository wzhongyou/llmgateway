package gateway

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/wzhongyou/llmgateway/core"
)

func LoadConfig(path string) (*core.GatewayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gateway: read config: %w", err)
	}

	var cfg core.GatewayConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("gateway: parse config: %w", err)
	}

	cfg.ApplyEnv()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}

	return &cfg, nil
}

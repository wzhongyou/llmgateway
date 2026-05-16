package server

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/wzhongyou/llmgate/core"
)

// Config is the full server configuration, extending core routing config with
// HTTP server settings.
type Config struct {
	Providers []core.ProviderConfig `toml:"providers"`
	Strategy  core.StrategyConfig   `toml:"strategy"`
	Server    ServerConfig          `toml:"server"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	ListenAddr   string   `toml:"listen_addr"`
	APIKeys      []string `toml:"api_keys"`
	RateLimitRPM int      `toml:"rate_limit_rpm"`
}

// coreConfig extracts the routing-only config for the sdk layer.
func (c *Config) coreConfig() *core.GatewayConfig {
	return &core.GatewayConfig{
		Providers: c.Providers,
		Strategy:  c.Strategy,
	}
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("server: read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("server: parse config: %w", err)
	}

	core.ApplyProviderEnv(cfg.Providers)

	if err := cfg.coreConfig().Validate(); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	return &cfg, nil
}

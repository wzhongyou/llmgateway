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
	Providers  []core.ProviderConfig `toml:"providers"`
	Strategy   core.StrategyConfig   `toml:"strategy"`
	Server     ServerConfig          `toml:"server"`

	configPath string
	keyRefs    map[string]string // provider name -> original TOML key value
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	ListenAddr   string   `toml:"listen_addr"`
	APIKeys      []string `toml:"api_keys"`
	RateLimitRPM int      `toml:"rate_limit_rpm"`
	AdminToken   string   `toml:"admin_token"`
}

// ConfigPath returns the path the config was loaded from.
func (c *Config) ConfigPath() string { return c.configPath }

// KeyRefs returns the original (pre-expansion) key values from the TOML file.
func (c *Config) KeyRefs() map[string]string { return c.keyRefs }

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

	// First pass: extract raw key values before env var expansion.
	keyRefs := extractKeyRefs(data)

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("server: parse config: %w", err)
	}
	cfg.configPath = path
	cfg.keyRefs = keyRefs

	core.ApplyProviderEnv(cfg.Providers)

	if err := cfg.coreConfig().Validate(); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	return &cfg, nil
}

// extractKeyRefs reads raw provider name->key mappings before env expansion.
func extractKeyRefs(data []byte) map[string]string {
	var raw struct {
		Providers []struct {
			Name string `toml:"name"`
			Key  string `toml:"key"`
		} `toml:"providers"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	refs := make(map[string]string, len(raw.Providers))
	for _, p := range raw.Providers {
		if p.Name != "" {
			refs[p.Name] = p.Key
		}
	}
	return refs
}

// SaveConfig writes the config to a TOML file, using raw key refs
// instead of the expanded values.
func SaveConfig(path string, cfg *Config) error {
	// Build a copy with original key references restored.
	saveCfg := *cfg
	refs := cfg.keyRefs
	saveCfg.Providers = make([]core.ProviderConfig, len(cfg.Providers))
	copy(saveCfg.Providers, cfg.Providers)
	for i := range saveCfg.Providers {
		if raw, ok := refs[saveCfg.Providers[i].Name]; ok && raw != "" {
			saveCfg.Providers[i].Key = raw
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("server: write config: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(saveCfg); err != nil {
		return fmt.Errorf("server: encode config: %w", err)
	}
	return nil
}

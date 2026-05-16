package core

import (
	"fmt"
	"os"
	"strings"
)

type GatewayConfig struct {
	Providers []ProviderConfig `toml:"providers"`
	Strategy  StrategyConfig   `toml:"strategy"`
}

type StrategyConfig struct {
	Primary            string   `toml:"primary"`
	Fallback           []string `toml:"fallback"`
	LatencyThresholdMs int64    `toml:"latency_threshold_ms"`
}

func ExpandEnv(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var buf strings.Builder
	i := 0
	n := len(s)
	for i < n {
		if i+1 < n && s[i] == '$' && s[i+1] == '{' {
			closing := strings.IndexByte(s[i+2:], '}')
			if closing < 0 {
				buf.WriteByte(s[i])
				i++
				continue
			}
			varName := s[i+2 : i+2+closing]
			buf.WriteString(os.Getenv(varName))
			i += 2 + closing + 1
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

func (c *GatewayConfig) ApplyEnv() {
	ApplyProviderEnv(c.Providers)
}

func ApplyProviderEnv(providers []ProviderConfig) {
	for i := range providers {
		providers[i].Key = ExpandEnv(providers[i].Key)
	}
}

func (c *GatewayConfig) Validate() error {
	active := 0
	for _, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("config: provider name is required")
		}
		if p.Key != "" {
			active++
		}
	}
	if active == 0 {
		return fmt.Errorf("config: at least one provider must have a key configured")
	}
	return nil
}

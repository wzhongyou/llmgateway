package core

import "fmt"

var registry = map[string]func(cfg ProviderConfig) (Provider, error){}
var envRegistry = map[string]string{}

type ProviderConfig struct {
	Name         string `toml:"name"`
	Key          string `toml:"key"`
	BaseURL      string `toml:"base_url"`
	DefaultModel string `toml:"default_model"`
	Protocol     string `toml:"protocol"` // "openai-compat" for custom providers via config
}

func RegisterProvider(name string, factory func(cfg ProviderConfig) (Provider, error)) {
	registry[name] = factory
}

// RegisterProviderEnv registers an environment variable → provider name mapping.
func RegisterProviderEnv(envVar, providerName string) {
	envRegistry[envVar] = providerName
}

// EnvProviders returns the registered env var → provider name mappings.
func EnvProviders() map[string]string {
	return envRegistry
}

func CreateProvider(cfg ProviderConfig) (Provider, error) {
	factory, ok := registry[cfg.Name]
	if !ok && cfg.Protocol == "openai-compat" {
		factory, ok = registry["openai-compat"]
	}
	if !ok {
		return nil, fmt.Errorf("core: unknown provider %q", cfg.Name)
	}
	return factory(cfg)
}

func RegisteredProviders() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

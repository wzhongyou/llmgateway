package core

import "fmt"

var registry = map[string]func(cfg ProviderConfig) (Provider, error){}

type ProviderConfig struct {
	Name         string `toml:"name"`
	Key          string `toml:"key"`
	BaseURL      string `toml:"base_url"`
	DefaultModel string `toml:"default_model"`
}

func RegisterProvider(name string, factory func(cfg ProviderConfig) (Provider, error)) {
	registry[name] = factory
}

func CreateProvider(cfg ProviderConfig) (Provider, error) {
	factory, ok := registry[cfg.Name]
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

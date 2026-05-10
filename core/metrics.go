package core

type MetricsSnapshot struct {
	Providers map[string]ProviderStats
}

type ProviderStats struct {
	ErrorRate    float64
	AvgLatencyMs float64
	Available    bool
}

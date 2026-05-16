package core

type MetricsSnapshot struct {
	Providers map[string]ProviderStats
}

type ProviderStats struct {
	TotalCalls   int64
	ErrorCalls   int64
	ErrorRate    float64
	AvgLatencyMs float64
	Available    bool
}

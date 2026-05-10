package core

import "time"

type PrimaryFirstStrategy struct {
	Primary  string
	Fallback []string
}

func (s *PrimaryFirstStrategy) Select(providers []Provider, req *ChatRequest, metrics *MetricsSnapshot) []Provider {
	index := make(map[string]Provider, len(providers))
	for _, p := range providers {
		index[p.Name()] = p
	}
	var ordered []Provider
	if p, ok := index[s.Primary]; ok {
		ordered = append(ordered, p)
	}
	for _, name := range s.Fallback {
		if name == s.Primary {
			continue
		}
		if p, ok := index[name]; ok {
			ordered = append(ordered, p)
		}
	}
	for _, p := range providers {
		if !containsProviderName(ordered, p.Name()) {
			ordered = append(ordered, p)
		}
	}
	return ordered
}

func containsProviderName(ps []Provider, name string) bool {
	for _, p := range ps {
		if p.Name() == name {
			return true
		}
	}
	return false
}

type LatencyStrategy struct {
	inner      Strategy
	thresholdMs float64
}

func NewLatencyStrategy(inner Strategy, thresholdMs float64) *LatencyStrategy {
	return &LatencyStrategy{inner: inner, thresholdMs: thresholdMs}
}

func (s *LatencyStrategy) Select(providers []Provider, req *ChatRequest, metrics *MetricsSnapshot) []Provider {
	ordered := s.inner.Select(providers, req, metrics)
	var filtered []Provider
	for _, p := range ordered {
		if metrics != nil {
			if stat, ok := metrics.Providers[p.Name()]; ok && stat.AvgLatencyMs >= s.thresholdMs {
				continue
			}
		}
		filtered = append(filtered, p)
	}
	if len(filtered) == 0 {
		return ordered
	}
	return filtered
}

type TimeBasedStrategy struct {
	dayProvider   string
	nightProvider string
}

func NewTimeBasedStrategy(dayProvider, nightProvider string) *TimeBasedStrategy {
	return &TimeBasedStrategy{dayProvider: dayProvider, nightProvider: nightProvider}
}

func (s *TimeBasedStrategy) Select(providers []Provider, req *ChatRequest, metrics *MetricsSnapshot) []Provider {
	now := time.Now()
	primary := s.dayProvider
	if now.Hour() < 8 || now.Hour() >= 20 {
		primary = s.nightProvider
	}
	index := make(map[string]Provider, len(providers))
	for _, p := range providers {
		index[p.Name()] = p
	}
	var ordered []Provider
	if p, ok := index[primary]; ok {
		ordered = append(ordered, p)
	}
	for _, p := range providers {
		if p.Name() != primary {
			ordered = append(ordered, p)
		}
	}
	return ordered
}

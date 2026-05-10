package core

import (
	"sync"
	"time"
)

type metricsStore struct {
	mu    sync.RWMutex
	stats map[string]*runningStats
}

type runningStats struct {
	totalCalls   int64
	errorCalls   int64
	totalLatency time.Duration
}

func newMetricsStore() *metricsStore {
	return &metricsStore{
		stats: make(map[string]*runningStats),
	}
}

func (m *metricsStore) ensure(name string) *runningStats {
	s, ok := m.stats[name]
	if !ok {
		s = &runningStats{}
		m.stats[name] = s
	}
	return s
}

func (m *metricsStore) recordSuccess(name string, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.ensure(name)
	s.totalCalls++
	s.totalLatency += latency
}

func (m *metricsStore) recordError(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.ensure(name)
	s.totalCalls++
	s.errorCalls++
}

func (m *metricsStore) snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ps := make(map[string]ProviderStats, len(m.stats))
	for name, s := range m.stats {
		var errRate float64
		var avgLatency float64
		if s.totalCalls > 0 {
			errRate = float64(s.errorCalls) / float64(s.totalCalls)
			avgLatency = float64(s.totalLatency.Microseconds()) / float64(s.totalCalls) / 1000.0
		}
		ps[name] = ProviderStats{
			ErrorRate:    errRate,
			AvgLatencyMs: avgLatency,
			Available:    true,
		}
	}
	return MetricsSnapshot{Providers: ps}
}

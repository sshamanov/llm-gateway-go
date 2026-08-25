package scheduler

import (
	"sync"
	"time"
)

// BackendModelKey identifies a unique backend+model combination.
type BackendModelKey struct {
	BackendID string
	ModelName string
}

// BackendModelStats tracks learned performance metrics for a backend+model pair.
type BackendModelStats struct {
	Samples             int64     `json:"samples"`
	AvgTokensPerSecond  float64   `json:"avg_tokens_per_second"`
	AvgColdLoadTime     float64   `json:"avg_cold_load_time"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastSuccess         time.Time `json:"last_success"`
	LastFailure         time.Time `json:"last_failure"`
}

// StatsTracker tracks EWMA-based performance stats per backend+model.
type StatsTracker struct {
	mu    sync.RWMutex
	stats map[BackendModelKey]*BackendModelStats
	alpha float64 // EWMA alpha, default 0.2
}

// NewStatsTracker creates a new StatsTracker with EWMA alpha=0.2.
func NewStatsTracker() *StatsTracker {
	return &StatsTracker{
		stats: make(map[BackendModelKey]*BackendModelStats),
		alpha: 0.2,
	}
}

// RecordSuccess records a successful request for a backend+model pair.
// ConsecutiveFailures is reset to 0.
// tokensPerSecond and coldLoadDurationSeconds are incorporated via EWMA:
//
//	new = alpha * latest + (1 - alpha) * old
func (st *StatsTracker) RecordSuccess(key BackendModelKey, tokensPerSecond float64, coldLoadDurationSeconds float64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.stats[key]
	if !ok {
		s = &BackendModelStats{}
		st.stats[key] = s
	}

	if s.Samples == 0 {
		s.AvgTokensPerSecond = tokensPerSecond
		s.AvgColdLoadTime = coldLoadDurationSeconds
	} else {
		s.AvgTokensPerSecond = st.alpha*tokensPerSecond + (1-st.alpha)*s.AvgTokensPerSecond
		s.AvgColdLoadTime = st.alpha*coldLoadDurationSeconds + (1-st.alpha)*s.AvgColdLoadTime
	}

	s.Samples++
	s.ConsecutiveFailures = 0
	s.LastSuccess = time.Now()
}

// RecordFailure records a failed request. ConsecutiveFailures is incremented by 1.
func (st *StatsTracker) RecordFailure(key BackendModelKey) {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.stats[key]
	if !ok {
		s = &BackendModelStats{}
		st.stats[key] = s
	}

	s.ConsecutiveFailures++
	s.LastFailure = time.Now()
}

// GetTokensPerSecond returns the learned tokens/sec for a key, or fallback if unknown.
func (st *StatsTracker) GetTokensPerSecond(key BackendModelKey, fallback float64) float64 {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.stats[key]
	if !ok || s.Samples == 0 {
		return fallback
	}
	return s.AvgTokensPerSecond
}

// GetColdLoadTime returns the learned cold load time for a key, or fallback if unknown.
func (st *StatsTracker) GetColdLoadTime(key BackendModelKey, fallback float64) float64 {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.stats[key]
	if !ok || s.Samples == 0 {
		return fallback
	}
	return s.AvgColdLoadTime
}

// GetSamples returns the number of recorded samples for a key.
func (st *StatsTracker) GetSamples(key BackendModelKey) int64 {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.stats[key]
	if !ok {
		return 0
	}
	return s.Samples
}

// GetConsecutiveFailures returns the consecutive failure count for a key.
func (st *StatsTracker) GetConsecutiveFailures(key BackendModelKey) int {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.stats[key]
	if !ok {
		return 0
	}
	return s.ConsecutiveFailures
}

// Prune removes stats rows for keys for which keep returns false. It is used to
// drop learned stats for models that no longer exist on their backend (e.g.
// removed from Ollama), so the /debug UI stops showing stale per-model rows.
func (st *StatsTracker) Prune(keep func(BackendModelKey) bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	for key := range st.stats {
		if !keep(key) {
			delete(st.stats, key)
		}
	}
}

// SnapshotAll returns a copy of all stats, safe for external reads.
func (st *StatsTracker) SnapshotAll() map[BackendModelKey]BackendModelStats {
	st.mu.RLock()
	defer st.mu.RUnlock()

	result := make(map[BackendModelKey]BackendModelStats, len(st.stats))
	for k, v := range st.stats {
		result[k] = *v
	}
	return result
}

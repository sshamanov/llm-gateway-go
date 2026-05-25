package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

// DebugSchedulerHandler returns an HTTP handler for GET /debug/scheduler.
func DebugSchedulerHandler(logger *logging.Logger, sched *scheduler.Scheduler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sched == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"scheduler": nil})
			return
		}
		stats := sched.Stats.SnapshotAll()
		queueSnap := sched.Queue.Snapshot(0)

		// Convert struct-keyed map to string-keyed for JSON encoding.
		stringStats := make(map[string]scheduler.BackendModelStats, len(stats))
		for k, v := range stats {
			stringStats[k.BackendID+"/"+k.ModelName] = v
		}

		resp := map[string]any{
			"uptime_seconds": time.Since(sched.StartedAt).Seconds(),
			"queue_total":    queueSnap.TotalJobs,
			"queue_pending":  queueSnap.PendingCount,
			"stats_count":    len(stats),
			"stats":          stringStats,
			"config": map[string]any{
				"strategy":          sched.Scorer.Config.Strategy,
				"queue_max":         sched.Scorer.Config.QueueMaxPending,
				"aging_per_sec":     sched.Scorer.Config.AgingPerSecond,
				"retry_max":         sched.Scorer.Config.Retry.MaxAttempts,
				"top_n_lookahead":   sched.Scorer.Config.TopNLookahead,
				"exploration_bonus": sched.Scorer.Config.ExplorationBonus,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"scheduler": resp})
	})
}

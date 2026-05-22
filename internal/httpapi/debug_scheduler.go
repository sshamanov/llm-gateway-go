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
		resp := map[string]any{
			"uptime_seconds": time.Since(sched.StartedAt).Seconds(),
			"queue_total":    queueSnap.TotalJobs,
			"queue_pending":  queueSnap.PendingCount,
			"stats_count":    len(stats),
			"stats":          stats,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"scheduler": resp})
	})
}

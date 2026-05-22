package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

// DebugQueueHandler returns an HTTP handler for GET /debug/queue.
func DebugQueueHandler(logger *logging.Logger, queue *scheduler.Queue) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if queue == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"queue": nil})
			return
		}
		snap := queue.Snapshot(50)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"queue": snap})
	})
}

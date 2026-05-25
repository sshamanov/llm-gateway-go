package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

// DebugBackendsHandler returns a handler that responds with JSON describing
// all backend states, augmented with active job counts from the lease manager.
func DebugBackendsHandler(logger *logging.Logger, registry *backend.Registry, backends *scheduler.BackendLeaseManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snapshots []backend.BackendSnapshot
		if registry != nil {
			snapshots = registry.BackendSnapshots()
		}
		if snapshots == nil {
			snapshots = make([]backend.BackendSnapshot, 0)
		}

		// Build active jobs map from backend lease manager.
		activeMap := make(map[string]int)
		if backends != nil {
			for _, s := range backends.States() {
				activeMap[s.BackendID] = s.ActiveJobs
			}
		}

		// Augment snapshots with active_jobs.
		type augmented struct {
			backend.BackendSnapshot
			ActiveJobs int `json:"active_jobs"`
		}
		result := make([]augmented, len(snapshots))
		for i, s := range snapshots {
			result[i] = augmented{
				BackendSnapshot: s,
				ActiveJobs:      activeMap[s.ID],
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"backends": result})
		logger.Debug("debug backends")
	})
}

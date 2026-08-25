package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

// debugHostEntry is one row in the GET /debug/hosts response. Status is derived
// from the health of the backends attached to the host (see backend.HostStatus).
type debugHostEntry struct {
	HostID     string `json:"host_id"`
	ActiveJobs int    `json:"active_jobs"`
	Capacity   int    `json:"capacity"`
	Status     string `json:"status"`
}

// DebugHostsHandler returns an HTTP handler for GET /debug/hosts.
// It combines host capacity state with backend health so a host whose backends
// are down is reported as such instead of "available".
func DebugHostsHandler(logger *logging.Logger, hosts *scheduler.HostLeaseManager, registry *backend.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hosts == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"hosts": nil})
			return
		}

		states := hosts.States()

		// Filter out the "default" host when explicit hosts are configured.
		if len(states) > 1 {
			filtered := make([]scheduler.HostState, 0, len(states)-1)
			for _, s := range states {
				if s.HostID != "default" {
					filtered = append(filtered, s)
				}
			}
			states = filtered
		}

		var snapshots []backend.BackendSnapshot
		if registry != nil {
			snapshots = registry.BackendSnapshots()
		}

		entries := make([]debugHostEntry, 0, len(states))
		for _, s := range states {
			entries = append(entries, debugHostEntry{
				HostID:     s.HostID,
				ActiveJobs: s.ActiveJobs,
				Capacity:   s.Capacity,
				Status:     backend.HostStatus(snapshots, s.HostID, s.ActiveJobs, s.Capacity),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"hosts": entries})
	})
}

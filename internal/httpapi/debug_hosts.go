package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

// DebugHostsHandler returns an HTTP handler for GET /debug/hosts.
func DebugHostsHandler(logger *logging.Logger, hosts *scheduler.HostLeaseManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hosts == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"hosts": nil})
			return
		}
		states := hosts.States()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"hosts": states})
	})
}

package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
)

// DebugBackendsHandler returns a handler that responds with JSON describing
// all backend states. Returns {"data":[]} if registry is nil.
func DebugBackendsHandler(logger *logging.Logger, registry *backend.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snapshots []backend.BackendSnapshot
		if registry != nil {
			snapshots = registry.BackendSnapshots()
		}
		if snapshots == nil {
			snapshots = make([]backend.BackendSnapshot, 0)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"data": snapshots})
		logger.Debug("debug backends")
	})
}

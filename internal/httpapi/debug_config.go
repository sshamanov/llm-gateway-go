package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
)

// DebugConfigHandler returns an HTTP handler for GET /debug/config.
func DebugConfigHandler(logger *logging.Logger, cfg *config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"config": nil})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"config": cfg})
	})
}

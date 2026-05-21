package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/logging"
)

// HealthHandler returns a handler that responds with {"status":"ok"} and a
// 200 status code on GET /healthz.
func HealthHandler(logger *logging.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		logger.Debug("health check ok")
	})
}

// ReadyHandler returns a handler that responds with {"status":"ready"} and a
// 200 status code on GET /readyz. In Milestone 1 it always reports ready.
func ReadyHandler(logger *logging.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
		logger.Debug("readiness check ok")
	})
}

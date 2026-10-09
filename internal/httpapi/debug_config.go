package httpapi

import (
	"encoding/json"
	"net/http"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
)

const redacted = "[REDACTED]"

// DebugConfigHandler returns an HTTP handler for GET /debug/config.
// Backend API keys are redacted from the response.
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
		json.NewEncoder(w).Encode(map[string]any{"config": redactConfig(cfg)})
	})
}

// redactConfig returns a copy of cfg with backend API keys masked.
func redactConfig(cfg *config.Config) config.Config {
	out := *cfg
	out.ImageBackends = append([]config.ImageBackendConfig(nil), cfg.ImageBackends...)
	for i := range out.ImageBackends {
		if out.ImageBackends[i].APIKey != "" {
			out.ImageBackends[i].APIKey = redacted
		}
	}
	out.AudioBackends = append([]config.AudioBackendConfig(nil), cfg.AudioBackends...)
	for i := range out.AudioBackends {
		if out.AudioBackends[i].APIKey != "" {
			out.AudioBackends[i].APIKey = redacted
		}
	}
	return out
}

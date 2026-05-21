package httpapi

import (
	"net/http"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/openai"
)

// NewRouter creates a new HTTP handler with all routes registered and
// middleware applied. If registry is nil, registry-dependent routes
// (/debug/backends, /debug/models, /v1/models) are not registered.
func NewRouter(logger *logging.Logger, registry *backend.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler(logger))
	mux.Handle("GET /readyz", ReadyHandler(logger))

	// Registry-dependent routes: only registered when registry is non-nil.
	if registry != nil {
		mux.Handle("GET /debug/backends", DebugBackendsHandler(logger, registry))
		mux.Handle("GET /debug/models", DebugModelsHandler(logger, registry))
		mux.Handle("GET /v1/models", openai.ModelsHandler(logger, registry))
		mux.Handle("POST /v1/chat/completions", openai.ChatCompletionsHandler(logger, registry))
	}

	var h http.Handler = mux
	h = RequestIDMiddleware(h)
	h = LoggingMiddleware(logger, h)
	return h
}

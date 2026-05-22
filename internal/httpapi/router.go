package httpapi

import (
	"net/http"

	"llm-go-proxy/internal/anthropic"
	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/openai"
	"llm-go-proxy/internal/scheduler"
	"llm-go-proxy/internal/storage"
)

// NewRouter creates a new HTTP handler with all routes registered and
// middleware applied. If registry is nil, registry-dependent routes
// (/debug/backends, /debug/models, /v1/models) are not registered.
func NewRouter(logger *logging.Logger, registry *backend.Registry, sched *scheduler.Scheduler, paths storage.Paths) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler(logger))
	mux.Handle("GET /readyz", ReadyHandler(logger))

	// Registry-dependent routes: only registered when registry is non-nil.
	if registry != nil {
		mux.Handle("GET /debug/backends", DebugBackendsHandler(logger, registry))
		mux.Handle("GET /debug/models", DebugModelsHandler(logger, registry))
		mux.Handle("GET /v1/models", openai.ModelsHandler(logger, registry))
		if sched != nil {
			mux.Handle("POST /v1/chat/completions", openai.ChatCompletionsHandler(logger, registry, sched))
			mux.Handle("POST /v1/responses", openai.ResponsesHandler(logger, registry, sched, paths.Uploads))
			mux.Handle("POST /v1/messages", anthropic.MessagesHandler(logger, registry, sched))
		}
		mux.Handle("POST /v1/messages/count_tokens", anthropic.CountTokensHandler(logger))
	}

	var h http.Handler = mux
	h = RequestIDMiddleware(h)
	h = LoggingMiddleware(logger, h)
	return h
}

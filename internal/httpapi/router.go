package httpapi

import (
	"net/http"

	"llm-go-proxy/internal/logging"
)

// NewRouter creates a new HTTP handler with all routes registered and
// middleware applied. The middleware order (outermost to innermost) is:
// LoggingMiddleware, RequestIDMiddleware, mux.
func NewRouter(logger *logging.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler(logger))
	mux.Handle("GET /readyz", ReadyHandler(logger))

	var h http.Handler = mux
	h = RequestIDMiddleware(h)
	h = LoggingMiddleware(logger, h)
	return h
}

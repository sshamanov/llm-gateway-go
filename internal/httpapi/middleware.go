package httpapi

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"time"

	"llm-go-proxy/internal/logging"
)

// contextKey is an unexported type for context keys to avoid collisions with
// keys defined in other packages.
type contextKey string

const requestIDKey contextKey = "request_id"

// RequestIDMiddleware generates a unique request ID for each request, sets it
// on the request context, and adds an X-Request-Id response header.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := generateRequestID()
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID extracts the request ID from the request's context. Returns an
// empty string if no request ID is set.
func GetRequestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

// generateRequestID creates a UUID-style string (e.g.
// "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx") using cryptographically random bytes.
func generateRequestID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// statusRecorder wraps an http.ResponseWriter and captures the HTTP status
// code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the wrapped writer.
func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware logs information about each completed HTTP request:
// method, path, status code, duration, and request ID. The request ID is
// read from the X-Request-Id response header, which is set by the inner
// RequestIDMiddleware.
func LoggingMiddleware(logger *logging.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)
		duration := time.Since(start)
		requestID := rec.Header().Get("X-Request-Id")
		logger.Info("request completed",
			logging.String("method", r.Method),
			logging.String("path", r.URL.Path),
			logging.Int("status", rec.statusCode),
			logging.Duration("duration", duration),
			logging.String("request_id", requestID),
		)
	})
}

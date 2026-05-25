package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
)

func TestDebugBackendsHandler_NilRegistry(t *testing.T) {
	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	handler := httpapi.DebugBackendsHandler(logger, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/debug/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	data, ok := body["backends"].([]interface{})
	if !ok {
		t.Fatalf("expected 'backends' to be an array, got %T", body["backends"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty backends array, got %d elements", len(data))
	}
}

func TestDebugBackendsHandler_ReturnsBackends(t *testing.T) {
	cfg := &config.Config{
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "b1", URL: "http://localhost:1", Host: "h1", MaxConcurrentRequests: 5, Enabled: true},
			{ID: "b2", URL: "http://localhost:2", Host: "h2", MaxConcurrentRequests: 3, Enabled: true},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	reg := backend.NewRegistry(cfg, logger)
	handler := httpapi.DebugBackendsHandler(logger, reg, nil)

	req := httptest.NewRequest(http.MethodGet, "/debug/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	data, ok := body["backends"].([]interface{})
	if !ok {
		t.Fatalf("expected 'backends' to be an array, got %T", body["backends"])
	}
	if len(data) != 2 {
		t.Errorf("expected 2 backends, got %d", len(data))
	}
}

func TestDebugBackendsHandler_ContentType(t *testing.T) {
	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	handler := httpapi.DebugBackendsHandler(logger, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/debug/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

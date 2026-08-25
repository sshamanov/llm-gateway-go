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
	"llm-go-proxy/internal/scheduler"
)

func newTestLogger() *logging.Logger {
	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)
	return logger
}

func testHostEntry(t *testing.T, resp *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct {
		Hosts []map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body.Hosts
}

// A registry that has never polled reports every enabled backend as unhealthy.
func TestDebugHostsHandler_DownHost(t *testing.T) {
	cfg := &config.Config{
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "b1", URL: "http://127.0.0.1:1", Host: "h1", Enabled: true},
			{ID: "b2", URL: "http://127.0.0.1:2", Host: "h1", Enabled: true},
		},
	}
	registry := backend.NewRegistry(cfg, newTestLogger())

	hosts := scheduler.NewHostLeaseManager([]config.HostConfig{
		{ID: "h1", MaxActiveJobs: 2},
		{ID: "h2", MaxActiveJobs: 1},
	})

	req := httptest.NewRequest(http.MethodGet, "/debug/hosts", nil)
	rec := httptest.NewRecorder()
	httpapi.DebugHostsHandler(newTestLogger(), hosts, registry).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	entries := testHostEntry(t, rec)
	byHost := make(map[string]string, len(entries))
	for _, e := range entries {
		byHost[e["host_id"].(string)] = e["status"].(string)
	}

	// h1 has two enabled backends that have never reported healthy -> down.
	if got := byHost["h1"]; got != "down" {
		t.Errorf("h1 status = %q, want %q", got, "down")
	}
	// h2 has no backends -> capacity-based.
	if got := byHost["h2"]; got != "available" {
		t.Errorf("h2 status = %q, want %q", got, "available")
	}
}

func TestDebugHostsHandler_NilRegistry(t *testing.T) {
	hosts := scheduler.NewHostLeaseManager([]config.HostConfig{
		{ID: "h1", MaxActiveJobs: 1},
	})

	req := httptest.NewRequest(http.MethodGet, "/debug/hosts", nil)
	rec := httptest.NewRecorder()
	httpapi.DebugHostsHandler(newTestLogger(), hosts, nil).ServeHTTP(rec, req)

	entries := testHostEntry(t, rec)
	if len(entries) != 1 {
		t.Fatalf("expected 1 host, got %d", len(entries))
	}
	if got := entries[0]["status"]; got != "available" {
		t.Errorf("status = %v, want %q", got, "available")
	}
}

func TestDebugHostsHandler_NilHosts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/debug/hosts", nil)
	rec := httptest.NewRecorder()
	httpapi.DebugHostsHandler(newTestLogger(), nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
)

// TestLoggingMiddlewareWrites verifies that LoggingMiddleware writes a JSON
// log entry to stdout when a request is handled.
func TestLoggingMiddlewareWrites(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	logger := logging.NewLogger(logging.LevelInfo, "test")
	logger.SetOutput(&stdout, &stderr)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := httpapi.LoggingMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if stdout.Len() == 0 {
		t.Fatal("expected log output on stdout")
	}
	if stderr.Len() > 0 {
		t.Error("unexpected output on stderr")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON in log output: %v", err)
	}

	if result["level"] != "info" {
		t.Errorf("level = %v, want %q", result["level"], "info")
	}
	if result["msg"] != "request completed" {
		t.Errorf("msg = %v, want %q", result["msg"], "request completed")
	}
	if result["method"] != "GET" {
		t.Errorf("method = %v, want %q", result["method"], "GET")
	}
	if result["path"] != "/test" {
		t.Errorf("path = %v, want %q", result["path"], "/test")
	}

	status, ok := result["status"].(float64)
	if !ok || int(status) != http.StatusOK {
		t.Errorf("status = %v, want %d", result["status"], http.StatusOK)
	}

	if _, ok := result["duration"]; !ok {
		t.Error("missing duration field in log entry")
	}

	if _, ok := result["request_id"]; !ok {
		t.Error("missing request_id field in log entry")
	}
}

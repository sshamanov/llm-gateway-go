package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/storage"
)

// TestRouterEndpoints is a table-driven test covering healthz, readyz, and
// 404 responses.
func TestRouterEndpoints(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(logging.LevelDebug, "test")
	handler := httpapi.NewRouter(logger, nil, nil, storage.Paths{})

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   map[string]string
	}{
		{
			name:       "GET /healthz returns 200 with status ok",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{"status": "ok"},
		},
		{
			name:       "GET /readyz returns 200 with status ready",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{"status": "ready"},
		},
		{
			name:       "POST /nonexistent returns 404",
			method:     http.MethodPost,
			path:       "/nonexistent",
			wantStatus: http.StatusNotFound,
			wantBody:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantBody != nil {
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("invalid JSON body: %v", err)
				}
				for k, v := range tt.wantBody {
					if body[k] != v {
						t.Errorf("body[%q] = %q, want %q", k, body[k], v)
					}
				}
			}
		})
	}
}

// TestResponseIncludesRequestID verifies that every response includes an
// X-Request-Id header.
func TestResponseIncludesRequestID(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(logging.LevelDebug, "test")
	handler := httpapi.NewRouter(logger, nil, nil, storage.Paths{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("response missing X-Request-Id header")
	}
}

// TestRequestIDFormat verifies that the request ID is a UUID-formatted string:
// 36 characters with hyphens in the standard positions.
func TestRequestIDFormat(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(logging.LevelDebug, "test")
	handler := httpapi.NewRouter(logger, nil, nil, storage.Paths{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-Id")
	if len(id) != 36 {
		t.Errorf("request ID length = %d, want 36", len(id))
	}
	if strings.Count(id, "-") != 4 {
		t.Errorf("request ID has %d hyphens, want 4", strings.Count(id, "-"))
	}

	const uuidPattern = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	if len(id) != len(uuidPattern) {
		t.Fatalf("unexpected request ID format: %q", id)
	}

	// Verify hyphens are at the correct positions (8-4-4-4-12).
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 parts, got %d", len(parts))
	}
	if len(parts[0]) != 8 {
		t.Errorf("first part length = %d, want 8", len(parts[0]))
	}
	if len(parts[1]) != 4 {
		t.Errorf("second part length = %d, want 4", len(parts[1]))
	}
	if len(parts[2]) != 4 {
		t.Errorf("third part length = %d, want 4", len(parts[2]))
	}
	if len(parts[3]) != 4 {
		t.Errorf("fourth part length = %d, want 4", len(parts[3]))
	}
	if len(parts[4]) != 12 {
		t.Errorf("fifth part length = %d, want 12", len(parts[4]))
	}
}

// TestUniqueRequestIDs verifies that two different requests receive different
// request IDs.
func TestUniqueRequestIDs(t *testing.T) {
	t.Parallel()

	logger := logging.NewLogger(logging.LevelDebug, "test")
	handler := httpapi.NewRouter(logger, nil, nil, storage.Paths{})

	ids := make([]string, 2)
	for i := range ids {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		ids[i] = rec.Header().Get("X-Request-Id")
	}

	if ids[0] == "" {
		t.Fatal("first request has no request ID")
	}
	if ids[1] == "" {
		t.Fatal("second request has no request ID")
	}
	if ids[0] == ids[1] {
		t.Error("two requests received the same request ID")
	}
}

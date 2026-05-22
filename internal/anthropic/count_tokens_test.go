package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-go-proxy/internal/logging"
)

// newDiscardLogger creates a logger that discards all output, for use in tests.
func newDiscardLogger() *logging.Logger {
	l := logging.NewLogger(logging.LevelDebug, "test")
	l.SetOutput(io.Discard, io.Discard)
	return l
}

func TestCountTokens_Basic(t *testing.T) {
	logger := newDiscardLogger()
	handler := CountTokensHandler(logger)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hello, world!"}]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result countTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0, got %d", result.InputTokens)
	}
}

func TestCountTokens_EmptyMessages(t *testing.T) {
	logger := newDiscardLogger()
	handler := CountTokensHandler(logger)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body := `{"model":"claude-sonnet-4-20250514","messages":[]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result countTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.InputTokens != 0 {
		t.Errorf("expected InputTokens 0, got %d", result.InputTokens)
	}
}

func TestCountTokens_WithSystem(t *testing.T) {
	logger := newDiscardLogger()
	handler := CountTokensHandler(logger)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// System field "abc" = 5 bytes as raw JSON (3 chars + 2 quote bytes).
	// Message content "a" = 3 bytes as raw JSON (1 char + 2 quote bytes).
	// Total = 8 bytes, 8/4 = 2.
	body := `{"model":"claude-sonnet-4-20250514","system":"abc","messages":[{"role":"user","content":"a"}]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result countTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.InputTokens != 2 {
		t.Errorf("expected InputTokens 2 (8 bytes / 4), got %d", result.InputTokens)
	}
}

func TestCountTokens_InvalidJSON(t *testing.T) {
	logger := newDiscardLogger()
	handler := CountTokensHandler(logger)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body := `{invalid json}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}
}

func TestCountTokens_Approximation(t *testing.T) {
	logger := newDiscardLogger()
	handler := CountTokensHandler(logger)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Create content with exactly 100 characters. In JSON, the raw message
	// value includes the surrounding quotes, so the content bytes will be
	// 102 (100 chars + 2 quote bytes). 102/4 = 25.
	content := strings.Repeat("a", 100)
	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"` + content + `"}]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result countTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.InputTokens != 25 {
		t.Errorf("expected InputTokens 25 (102 bytes / 4), got %d", result.InputTokens)
	}
}

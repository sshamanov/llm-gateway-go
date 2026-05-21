package ollama

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchTags_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		resp := TagsResponse{
			Models: []TagsModel{
				{
					Name:       "llama3:8b",
					ModifiedAt: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
					Size:       4712345678,
				},
				{
					Name:       "mistral:7b",
					ModifiedAt: time.Date(2025, 2, 20, 14, 0, 0, 0, time.UTC),
					Size:       4123456789,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := server.Client()
	result, err := FetchTags(client, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if len(result.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Models))
	}

	if result.Models[0].Name != "llama3:8b" {
		t.Errorf("expected Name 'llama3:8b', got %q", result.Models[0].Name)
	}
	if result.Models[0].Size != 4712345678 {
		t.Errorf("expected Size 4712345678, got %d", result.Models[0].Size)
	}

	if result.Models[1].Name != "mistral:7b" {
		t.Errorf("expected Name 'mistral:7b', got %q", result.Models[1].Name)
	}
	if result.Models[1].Size != 4123456789 {
		t.Errorf("expected Size 4123456789, got %d", result.Models[1].Size)
	}
}

func TestFetchTags_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := server.Client()
	_, err := FetchTags(client, server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got %q", err.Error())
	}
}

func TestFetchTags_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json}`))
	}))
	defer server.Close()

	client := server.Client()
	_, err := FetchTags(client, server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFetchTags_Unreachable(t *testing.T) {
	// Create a listener that we immediately close to get an unreachable address.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err = FetchTags(client, "http://"+addr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFetchTags_TrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := TagsResponse{
			Models: []TagsModel{
				{Name: "test-model", Size: 1234},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := server.Client()
	// Use baseURL with a trailing slash.
	result, err := FetchTags(client, server.URL+"/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}
	if result.Models[0].Name != "test-model" {
		t.Errorf("expected Name 'test-model', got %q", result.Models[0].Name)
	}
}

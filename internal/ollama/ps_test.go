package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchPS_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		resp := PSResponse{
			Models: []PSModel{
				{
					Name:   "llama3:8b",
					Model:  "llama3:8b",
					Size:   4712345678,
					Digest: "sha256:a1b2c3d4e5f6",
					Details: ModelDetail{
						ParentModel:       "llama3",
						Format:            "gguf",
						Family:            "llama",
						Families:          []string{"llama"},
						ParameterSize:     "8.0B",
						QuantizationLevel: "Q4_K_M",
					},
					ExpiresAt: time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC),
					SizeVRAM:  4123456789,
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
	result, err := FetchPS(client, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if len(result.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result.Models))
	}

	m := result.Models[0]
	if m.Name != "llama3:8b" {
		t.Errorf("expected Name 'llama3:8b', got %q", m.Name)
	}
	if m.Model != "llama3:8b" {
		t.Errorf("expected Model 'llama3:8b', got %q", m.Model)
	}
	if m.Size != 4712345678 {
		t.Errorf("expected Size 4712345678, got %d", m.Size)
	}
	if m.Digest != "sha256:a1b2c3d4e5f6" {
		t.Errorf("expected Digest 'sha256:a1b2c3d4e5f6', got %q", m.Digest)
	}
	if m.SizeVRAM != 4123456789 {
		t.Errorf("expected SizeVRAM 4123456789, got %d", m.SizeVRAM)
	}

	if m.Details.ParentModel != "llama3" {
		t.Errorf("expected ParentModel 'llama3', got %q", m.Details.ParentModel)
	}
	if m.Details.Format != "gguf" {
		t.Errorf("expected Format 'gguf', got %q", m.Details.Format)
	}
	if m.Details.Family != "llama" {
		t.Errorf("expected Family 'llama', got %q", m.Details.Family)
	}
	if len(m.Details.Families) != 1 || m.Details.Families[0] != "llama" {
		t.Errorf("expected Families ['llama'], got %v", m.Details.Families)
	}
	if m.Details.ParameterSize != "8.0B" {
		t.Errorf("expected ParameterSize '8.0B', got %q", m.Details.ParameterSize)
	}
	if m.Details.QuantizationLevel != "Q4_K_M" {
		t.Errorf("expected QuantizationLevel 'Q4_K_M', got %q", m.Details.QuantizationLevel)
	}
}

func TestFetchPS_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := server.Client()
	_, err := FetchPS(client, server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got %q", err.Error())
	}
}

func TestFetchPS_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json}`))
	}))
	defer server.Close()

	client := server.Client()
	_, err := FetchPS(client, server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

package ollama

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTagsResponseUnmarshal(t *testing.T) {
	data := `{
		"models": [
			{
				"name": "llama3:8b",
				"modified_at": "2025-01-15T10:30:00Z",
				"size": 4712345678
			},
			{
				"name": "mistral:7b",
				"modified_at": "2025-02-20T14:00:00Z",
				"size": 4123456789
			}
		]
	}`

	var resp TagsResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		t.Fatalf("unexpected error unmarshaling TagsResponse: %v", err)
	}

	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Models))
	}

	expectedModified, _ := time.Parse(time.RFC3339, "2025-01-15T10:30:00Z")
	if !resp.Models[0].ModifiedAt.Equal(expectedModified) {
		t.Errorf("expected ModifiedAt %v, got %v", expectedModified, resp.Models[0].ModifiedAt)
	}

	if resp.Models[0].Name != "llama3:8b" {
		t.Errorf("expected Name 'llama3:8b', got %q", resp.Models[0].Name)
	}
	if resp.Models[0].Size != 4712345678 {
		t.Errorf("expected Size 4712345678, got %d", resp.Models[0].Size)
	}

	if resp.Models[1].Name != "mistral:7b" {
		t.Errorf("expected Name 'mistral:7b', got %q", resp.Models[1].Name)
	}
	if resp.Models[1].Size != 4123456789 {
		t.Errorf("expected Size 4123456789, got %d", resp.Models[1].Size)
	}
}

func TestPSResponseUnmarshal(t *testing.T) {
	data := `{
		"models": [
			{
				"name": "llama3:8b",
				"model": "llama3:8b",
				"size": 4712345678,
				"digest": "sha256:a1b2c3d4e5f6",
				"details": {
					"parent_model": "llama3",
					"format": "gguf",
					"family": "llama",
					"families": ["llama"],
					"parameter_size": "8.0B",
					"quantization_level": "Q4_K_M"
				},
				"expires_at": "2025-03-01T12:00:00Z",
				"size_vram": 4123456789
			}
		]
	}`

	var resp PSResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		t.Fatalf("unexpected error unmarshaling PSResponse: %v", err)
	}

	if len(resp.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Models))
	}

	m := resp.Models[0]
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

	expectedExpires, _ := time.Parse(time.RFC3339, "2025-03-01T12:00:00Z")
	if !m.ExpiresAt.Equal(expectedExpires) {
		t.Errorf("expected ExpiresAt %v, got %v", expectedExpires, m.ExpiresAt)
	}

	d := m.Details
	if d.ParentModel != "llama3" {
		t.Errorf("expected ParentModel 'llama3', got %q", d.ParentModel)
	}
	if d.Format != "gguf" {
		t.Errorf("expected Format 'gguf', got %q", d.Format)
	}
	if d.Family != "llama" {
		t.Errorf("expected Family 'llama', got %q", d.Family)
	}
	if len(d.Families) != 1 || d.Families[0] != "llama" {
		t.Errorf("expected Families ['llama'], got %v", d.Families)
	}
	if d.ParameterSize != "8.0B" {
		t.Errorf("expected ParameterSize '8.0B', got %q", d.ParameterSize)
	}
	if d.QuantizationLevel != "Q4_K_M" {
		t.Errorf("expected QuantizationLevel 'Q4_K_M', got %q", d.QuantizationLevel)
	}
}

func TestTagsResponseMarshal(t *testing.T) {
	original := TagsResponse{
		Models: []TagsModel{
			{
				Name:       "llama3:8b",
				ModifiedAt: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
				Size:       4712345678,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("unexpected error marshaling TagsResponse: %v", err)
	}

	var roundTripped TagsResponse
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unexpected error unmarshaling TagsResponse: %v", err)
	}

	if len(roundTripped.Models) != len(original.Models) {
		t.Fatalf("expected %d models, got %d", len(original.Models), len(roundTripped.Models))
	}

	if roundTripped.Models[0].Name != original.Models[0].Name {
		t.Errorf("expected Name %q, got %q", original.Models[0].Name, roundTripped.Models[0].Name)
	}
	if roundTripped.Models[0].Size != original.Models[0].Size {
		t.Errorf("expected Size %d, got %d", original.Models[0].Size, roundTripped.Models[0].Size)
	}
}

func TestPSResponseMarshal(t *testing.T) {
	original := PSResponse{
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

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("unexpected error marshaling PSResponse: %v", err)
	}

	var roundTripped PSResponse
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unexpected error unmarshaling PSResponse: %v", err)
	}

	if len(roundTripped.Models) != len(original.Models) {
		t.Fatalf("expected %d models, got %d", len(original.Models), len(roundTripped.Models))
	}

	rt := roundTripped.Models[0]
	o := original.Models[0]

	if rt.Name != o.Name {
		t.Errorf("expected Name %q, got %q", o.Name, rt.Name)
	}
	if rt.Model != o.Model {
		t.Errorf("expected Model %q, got %q", o.Model, rt.Model)
	}
	if rt.Size != o.Size {
		t.Errorf("expected Size %d, got %d", o.Size, rt.Size)
	}
	if rt.Digest != o.Digest {
		t.Errorf("expected Digest %q, got %q", o.Digest, rt.Digest)
	}
	if rt.SizeVRAM != o.SizeVRAM {
		t.Errorf("expected SizeVRAM %d, got %d", o.SizeVRAM, rt.SizeVRAM)
	}

	// Compare time equality via RFC3339 string to avoid location/precision issues.
	if rt.ExpiresAt.Format(time.RFC3339) != o.ExpiresAt.Format(time.RFC3339) {
		t.Errorf("expected ExpiresAt %v, got %v", o.ExpiresAt, rt.ExpiresAt)
	}

	if rt.Details.ParentModel != o.Details.ParentModel {
		t.Errorf("expected ParentModel %q, got %q", o.Details.ParentModel, rt.Details.ParentModel)
	}
	if rt.Details.Format != o.Details.Format {
		t.Errorf("expected Format %q, got %q", o.Details.Format, rt.Details.Format)
	}
	if rt.Details.Family != o.Details.Family {
		t.Errorf("expected Family %q, got %q", o.Details.Family, rt.Details.Family)
	}
	if len(rt.Details.Families) != len(o.Details.Families) || rt.Details.Families[0] != o.Details.Families[0] {
		t.Errorf("expected Families %v, got %v", o.Details.Families, rt.Details.Families)
	}
	if rt.Details.ParameterSize != o.Details.ParameterSize {
		t.Errorf("expected ParameterSize %q, got %q", o.Details.ParameterSize, rt.Details.ParameterSize)
	}
	if rt.Details.QuantizationLevel != o.Details.QuantizationLevel {
		t.Errorf("expected QuantizationLevel %q, got %q", o.Details.QuantizationLevel, rt.Details.QuantizationLevel)
	}
}

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", client.Timeout)
	}
	// Verify it's a usable *http.Client.
	var _ *http.Client = client
}

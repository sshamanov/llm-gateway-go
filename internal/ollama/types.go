package ollama

import "time"

// TagsResponse is the response from GET /api/tags.
type TagsResponse struct {
	Models []TagsModel `json:"models"`
}

// TagsModel represents a single model in the tags list.
type TagsModel struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// PSResponse is the response from GET /api/ps.
type PSResponse struct {
	Models []PSModel `json:"models"`
}

// PSModel represents a single running model returned by /api/ps.
type PSModel struct {
	Name      string      `json:"name"`
	Model     string      `json:"model"`
	Size      int64       `json:"size"`
	Digest    string      `json:"digest"`
	Details   ModelDetail `json:"details"`
	ExpiresAt time.Time   `json:"expires_at"`
	SizeVRAM  int64       `json:"size_vram"`
}

// ModelDetail holds detailed model metadata.
type ModelDetail struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

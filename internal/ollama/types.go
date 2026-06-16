package ollama

import (
	"encoding/json"
	"time"
)

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

// ChatRequest is the request body for Ollama's POST /api/chat.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	// Think is optional; nil means "use Ollama default".
	Think     *bool           `json:"think,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   *ChatOptions    `json:"options,omitempty"`
	Tools     json.RawMessage `json:"tools,omitempty"`
}

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// ChatOptions maps to the "options" object in the Ollama /api/chat request.
type ChatOptions struct {
	NumThread      int      `json:"num_thread,omitempty"`
	NumCtx         int      `json:"num_ctx,omitempty"`
	Temperature    float64  `json:"temperature,omitempty"`
	TopP           float64  `json:"top_p,omitempty"`
	TopK           int      `json:"top_k,omitempty"`
	RepeatPenalty  float64  `json:"repeat_penalty,omitempty"`
	NumPredict     int      `json:"num_predict,omitempty"`
	Stop           []string `json:"stop,omitempty"`
	UseMmap        *bool    `json:"use_mmap,omitempty"`
}

// ChatResponse is the response from Ollama's POST /api/chat (non-streaming).
type ChatResponse struct {
	Model     string      `json:"model"`
	CreatedAt time.Time   `json:"created_at"`
	Message   ChatMessage `json:"message"`
	Done      bool        `json:"done"`
	// Metrics populated when done=true.
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	DoneReason         string `json:"done_reason,omitempty"`
}

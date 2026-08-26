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

func TestChatRequestUnmarshal(t *testing.T) {
	think := true
	data := `{
		"model": "llama3:8b",
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi"}
		],
		"stream": true,
		"think": true,
		"keep_alive": "5m",
		"options": {
			"num_thread": 4,
			"num_ctx": 4096,
			"temperature": 0.7,
			"top_p": 0.9,
			"num_predict": 128,
			"stop": ["\n", "user:"]
		}
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error unmarshaling ChatRequest: %v", err)
	}

	if req.Model != "llama3:8b" {
		t.Errorf("expected Model 'llama3:8b', got %q", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" || req.Messages[0].Content != "hello" {
		t.Errorf("expected first message 'user: hello', got %q: %q", req.Messages[0].Role, req.Messages[0].Content)
	}
	if req.Messages[1].Role != "assistant" || req.Messages[1].Content != "hi" {
		t.Errorf("expected second message 'assistant: hi', got %q: %q", req.Messages[1].Role, req.Messages[1].Content)
	}
	if !req.Stream {
		t.Errorf("expected Stream true")
	}
	if req.Think == nil || *req.Think != think {
		t.Errorf("expected Think true, got %v", req.Think)
	}
	if req.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", req.KeepAlive)
	}
	if req.Options.NumThread != 4 {
		t.Errorf("expected Options.NumThread 4, got %d", req.Options.NumThread)
	}
	if req.Options.NumCtx != 4096 {
		t.Errorf("expected Options.NumCtx 4096, got %d", req.Options.NumCtx)
	}
	if req.Options.Temperature != 0.7 {
		t.Errorf("expected Options.Temperature 0.7, got %f", req.Options.Temperature)
	}
	if req.Options.TopP != 0.9 {
		t.Errorf("expected Options.TopP 0.9, got %f", req.Options.TopP)
	}
	if req.Options.NumPredict != 128 {
		t.Errorf("expected Options.NumPredict 128, got %d", req.Options.NumPredict)
	}
	if len(req.Options.Stop) != 2 || req.Options.Stop[0] != "\n" || req.Options.Stop[1] != "user:" {
		t.Errorf("expected Options.Stop ['\\n', 'user:'], got %v", req.Options.Stop)
	}
}

func TestChatRequestMarshal(t *testing.T) {
	think := true
	original := ChatRequest{
		Model:    "llama3:8b",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
		Stream:   true,
		Think:    &think,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("unexpected error marshaling ChatRequest: %v", err)
	}

	var roundTripped ChatRequest
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unexpected error unmarshaling ChatRequest: %v", err)
	}

	if roundTripped.Model != original.Model {
		t.Errorf("expected Model %q, got %q", original.Model, roundTripped.Model)
	}
	if len(roundTripped.Messages) != len(original.Messages) {
		t.Errorf("expected %d messages, got %d", len(original.Messages), len(roundTripped.Messages))
	}
	if roundTripped.Messages[0].Role != original.Messages[0].Role {
		t.Errorf("expected Role %q, got %q", original.Messages[0].Role, roundTripped.Messages[0].Role)
	}
	if roundTripped.Messages[0].Content != original.Messages[0].Content {
		t.Errorf("expected Content %q, got %q", original.Messages[0].Content, roundTripped.Messages[0].Content)
	}
	if roundTripped.Stream != original.Stream {
		t.Errorf("expected Stream %v, got %v", original.Stream, roundTripped.Stream)
	}
	if roundTripped.Think == nil || *roundTripped.Think != *original.Think {
		t.Errorf("expected Think %v, got %v", *original.Think, roundTripped.Think)
	}
}

func TestChatResponseUnmarshal(t *testing.T) {
	data := `{
		"model": "llama3:8b",
		"created_at": "2025-03-01T12:00:00Z",
		"message": {
			"role": "assistant",
			"content": "Hello! How can I help you today?"
		},
		"done": true,
		"total_duration": 123456789,
		"load_duration": 50000000,
		"prompt_eval_count": 42,
		"eval_count": 150,
		"done_reason": "stop"
	}`

	var resp ChatResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		t.Fatalf("unexpected error unmarshaling ChatResponse: %v", err)
	}

	if resp.Model != "llama3:8b" {
		t.Errorf("expected Model 'llama3:8b', got %q", resp.Model)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2025-03-01T12:00:00Z")
	if !resp.CreatedAt.Equal(expectedTime) {
		t.Errorf("expected CreatedAt %v, got %v", expectedTime, resp.CreatedAt)
	}

	if resp.Message.Role != "assistant" {
		t.Errorf("expected Message.Role 'assistant', got %q", resp.Message.Role)
	}
	if resp.Message.Content != "Hello! How can I help you today?" {
		t.Errorf("expected Message.Content 'Hello! How can I help you today?', got %q", resp.Message.Content)
	}
	if !resp.Done {
		t.Errorf("expected Done true")
	}
	if resp.TotalDuration != 123456789 {
		t.Errorf("expected TotalDuration 123456789, got %d", resp.TotalDuration)
	}
	if resp.LoadDuration != 50000000 {
		t.Errorf("expected LoadDuration 50000000, got %d", resp.LoadDuration)
	}
	if resp.PromptEvalCount != 42 {
		t.Errorf("expected PromptEvalCount 42, got %d", resp.PromptEvalCount)
	}
	if resp.EvalCount != 150 {
		t.Errorf("expected EvalCount 150, got %d", resp.EvalCount)
	}
	if resp.DoneReason != "stop" {
		t.Errorf("expected DoneReason 'stop', got %q", resp.DoneReason)
	}
}

func TestChatRequest_OptionalFields(t *testing.T) {
	req := ChatRequest{
		Model:    "llama3:8b",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
		Stream:   true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("unexpected error marshaling ChatRequest: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unexpected error unmarshaling into map: %v", err)
	}

	if _, ok := result["think"]; ok {
		t.Error("expected 'think' to be omitted from JSON output")
	}
	if _, ok := result["keep_alive"]; ok {
		t.Error("expected 'keep_alive' to be omitted from JSON output")
	}
	if _, ok := result["options"]; ok {
		t.Error("expected 'options' to be omitted from JSON output")
	}
	if _, ok := result["tools"]; ok {
		t.Error("expected 'tools' to be omitted from JSON output")
	}
}

func TestChatResponse_OptionalFieldsOmitted(t *testing.T) {
	resp := ChatResponse{
		Model:     "llama3:8b",
		CreatedAt: time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC),
		Message:   ChatMessage{Role: "assistant", Content: "Hello!"},
		Done:      true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("unexpected error marshaling ChatResponse: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unexpected error unmarshaling into map: %v", err)
	}

	if _, ok := result["total_duration"]; ok {
		t.Error("expected 'total_duration' to be omitted from JSON output")
	}
	if _, ok := result["load_duration"]; ok {
		t.Error("expected 'load_duration' to be omitted from JSON output")
	}
	if _, ok := result["prompt_eval_count"]; ok {
		t.Error("expected 'prompt_eval_count' to be omitted from JSON output")
	}
	if _, ok := result["eval_count"]; ok {
		t.Error("expected 'eval_count' to be omitted from JSON output")
	}
	if _, ok := result["done_reason"]; ok {
		t.Error("expected 'done_reason' to be omitted from JSON output")
	}
}

func TestChatMessageToolCallsRoundTrip(t *testing.T) {
	msg := ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []ChatToolCall{
			{
				Function: ChatToolCallFunction{
					Name:      "get_weather",
					Arguments: json.RawMessage(`{"location":"SF"}`),
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("unexpected error marshaling ChatMessage: %v", err)
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unexpected error unmarshaling into map: %v", err)
	}

	if _, ok := out["tool_calls"]; !ok {
		t.Fatalf("expected 'tool_calls' in JSON output, got %s", data)
	}

	var back ChatMessage
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unexpected error unmarshaling ChatMessage: %v", err)
	}
	if len(back.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(back.ToolCalls))
	}
	if back.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", back.ToolCalls[0].Function.Name)
	}
	if string(back.ToolCalls[0].Function.Arguments) != `{"location":"SF"}` {
		t.Errorf("expected arguments `{\"location\":\"SF\"}`, got %s", back.ToolCalls[0].Function.Arguments)
	}
}

package scenarios

import (
	"encoding/json"
	"fmt"
	"strings"

	"llm-go-proxy/internal/mock"
)

// HealthyCluster runs a non-streaming chat request and verifies the response shape.
func HealthyCluster(h *mock.Harness) error {
	req := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	resp, body, err := h.Post("/v1/chat/completions", req)
	if err != nil {
		return fmt.Errorf("POST failed: %w", err)
	}
	if err := h.AssertStatus(200, resp); err != nil {
		return err
	}

	// Check required response fields.
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if _, ok := data["id"]; !ok {
		return fmt.Errorf("missing 'id' field in response")
	}
	if _, ok := data["choices"]; !ok {
		return fmt.Errorf("missing 'choices' field in response")
	}
	if _, ok := data["model"]; !ok {
		return fmt.Errorf("missing 'model' field in response")
	}

	return nil
}

// StreamingChat runs a streaming chat request and verifies SSE chunks.
func StreamingChat(h *mock.Harness) error {
	req := map[string]interface{}{
		"model":    "llama3",
		"stream":   true,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	lines, err := h.GetStream("/v1/chat/completions", req)
	if err != nil {
		return fmt.Errorf("streaming request failed: %w", err)
	}

	if len(lines) == 0 {
		return fmt.Errorf("no SSE data lines received")
	}

	hasDone := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "data: [DONE]" {
			hasDone = true
			break
		}
	}

	if !hasDone {
		return fmt.Errorf("missing [DONE] sentinel in stream")
	}

	return nil
}

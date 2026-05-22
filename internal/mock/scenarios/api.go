package scenarios

import (
	"encoding/json"
	"fmt"

	"llm-go-proxy/internal/mock"
)

// FullAPISurface sends one request to each major API endpoint and verifies
// the response shape.
func FullAPISurface(h *mock.Harness) error {
	chatReq := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	// /v1/chat/completions
	resp, body, err := h.Post("/v1/chat/completions", chatReq)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("chat: expected 200, got %d", resp.StatusCode)
	}
	var chatData map[string]interface{}
	json.Unmarshal(body, &chatData)
	if chatData["id"] == nil {
		return fmt.Errorf("chat: missing id")
	}

	// /v1/responses
	respReq := map[string]interface{}{
		"model": "llama3",
		"input": "Hello",
	}
	resp, body, err = h.Post("/v1/responses", respReq)
	if err != nil {
		return fmt.Errorf("responses: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("responses: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}

	// /v1/messages
	msgReq := map[string]interface{}{
		"model":      "llama3",
		"max_tokens": 100,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}
	resp, body, err = h.Post("/v1/messages", msgReq)
	if err != nil {
		return fmt.Errorf("messages: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("messages: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}

	// /v1/messages/count_tokens
	countReq := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}
	resp, _, err = h.Post("/v1/messages/count_tokens", countReq)
	if err != nil {
		return fmt.Errorf("count_tokens: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("count_tokens: expected 200, got %d", resp.StatusCode)
	}

	// /v1/models
	resp, body, err = h.Get("/v1/models")
	if err != nil {
		return fmt.Errorf("models: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("models: expected 200, got %d", resp.StatusCode)
	}
	var modelsData map[string]interface{}
	json.Unmarshal(body, &modelsData)
	if modelsData["data"] == nil {
		return fmt.Errorf("models: missing data field")
	}

	return nil
}

// ImageGeneration verifies the image generation endpoint.
func ImageGeneration(h *mock.Harness) error {
	imgReq := map[string]interface{}{
		"prompt": "A blue sky",
		"n":      1,
	}

	resp, body, err := h.Post("/v1/images/generations", imgReq)
	if err != nil {
		return fmt.Errorf("image gen: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("image gen: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("image gen: unmarshal: %w", err)
	}
	if data["created"] == nil {
		return fmt.Errorf("image gen: missing 'created' field")
	}
	if data["data"] == nil {
		return fmt.Errorf("image gen: missing 'data' field")
	}

	return nil
}

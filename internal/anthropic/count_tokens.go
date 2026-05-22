package anthropic

import (
	"encoding/json"
	"io"
	"net/http"

	"llm-go-proxy/internal/logging"
)

const maxCountTokensBodySize = 10 * 1024 * 1024

// countTokensRequest is the request body for POST /v1/messages/count_tokens.
type countTokensRequest struct {
	Model    string                   `json:"model"`
	Messages []anthropicInputMessage  `json:"messages"`
	System   json.RawMessage          `json:"system,omitempty"`
}

// countTokensResponse is the response body for POST /v1/messages/count_tokens.
type countTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// CountTokensHandler returns an HTTP handler for POST /v1/messages/count_tokens.
// It computes input_tokens as the total byte length of all message content
// divided by 4 (simple approximation per ARCH §14.4).
func CountTokensHandler(logger *logging.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxCountTokensBodySize))
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
			return
		}

		// Decode JSON.
		var req countTokensRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
			return
		}

		// Count bytes from all message content.
		var totalBytes int
		for _, msg := range req.Messages {
			totalBytes += len(msg.Content)
		}

		// Count bytes from system field.
		if len(req.System) > 0 {
			totalBytes += len(req.System)
		}

		// bytes / 4 approximation.
		inputTokens := totalBytes / 4

		// Write response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(countTokensResponse{InputTokens: inputTokens})
	})
}

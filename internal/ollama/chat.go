package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SendChat sends a chat request to an Ollama backend and returns the response.
// It POSTs to {baseURL}/api/chat. The context controls the lifetime of the
// HTTP request.
func SendChat(ctx context.Context, client *http.Client, baseURL string, req *ChatRequest) (*ChatResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/chat"

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("send chat: encode request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("send chat: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send chat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("send chat: unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("send chat: read body: %w", err)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("send chat: decode body: %w", err)
	}

	return &chatResp, nil
}

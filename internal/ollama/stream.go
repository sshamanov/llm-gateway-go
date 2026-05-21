package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamChunk carries one JSON line from an Ollama streaming chat response.
type StreamChunk struct {
	Response *ChatResponse // non-nil for content chunks; nil when Err is set
	Err      error         // non-nil only for terminal transport errors
}

// SendChatStream sends a chat request with stream=true and returns a channel
// of parsed JSON lines. The returned channel is closed when the stream ends
// (either normally, on error, or on context cancellation).
//
// The caller MUST drain the channel until it closes.
// The caller should create the result channel before calling this function
// so it owns the channel lifecycle.
func SendChatStream(ctx context.Context, client *http.Client, baseURL string, req *ChatRequest) (<-chan StreamChunk, error) {
	req.Stream = true

	url := strings.TrimRight(baseURL, "/") + "/api/chat"

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("send chat stream: encode request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("send chat stream: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send chat stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("send chat stream: unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	ch := make(chan StreamChunk, 10)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var chatResp ChatResponse
			if err := json.Unmarshal([]byte(line), &chatResp); err != nil {
				ch <- StreamChunk{Err: fmt.Errorf("stream: decode line: %w", err)}
				return
			}

			ch <- StreamChunk{Response: &chatResp}
		}

		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				return
			}
			ch <- StreamChunk{Err: fmt.Errorf("stream: read: %w", err)}
		}
	}()

	return ch, nil
}

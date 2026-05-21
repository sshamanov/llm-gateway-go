package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sseChatChunk is the per-chunk SSE data payload for streaming chat completions.
type sseChatChunk struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []sseChatChoice `json:"choices"`
}

// sseChatChoice is a single completion choice delta in an SSE chunk.
type sseChatChoice struct {
	Index        int          `json:"index"`
	Delta        sseChatDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason,omitempty"`
	Usage        *sseUsage    `json:"usage,omitempty"`
}

// sseChatDelta represents the delta content in a streaming chat chunk.
type sseChatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// sseUsage holds token usage statistics (only present in final chunk).
type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// writeSSEChatChunk writes a single SSE data event for a chat completion chunk.
// role is only non-empty for the first content chunk.
// finishReason is only non-empty for the terminal chunk.
// usage is only non-nil for the terminal chunk.
// Returns any error from writing or flushing.
func writeSSEChatChunk(w io.Writer, id, model string, created int64, role, content string, finishReason string, usage *sseUsage) error {
	chunk := sseChatChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []sseChatChoice{
			{
				Index: 0,
				Delta: sseChatDelta{
					Role:    role,
					Content: content,
				},
			},
		},
	}

	if finishReason != "" {
		fr := finishReason
		chunk.Choices[0].FinishReason = &fr
		chunk.Choices[0].Usage = usage
	}

	jsonBytes, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonBytes); err != nil {
		return err
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// writeSSEDone writes the SSE [DONE] marker and flushes.
func writeSSEDone(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "data: [DONE]\n\n"); err != nil {
		return err
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

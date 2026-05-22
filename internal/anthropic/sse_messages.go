package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sseContentBlockStart is the first event in an Anthropic streaming response.
type sseContentBlockStart struct {
	Type         string                `json:"type"`          // "content_block_start"
	Index        int                   `json:"index"`
	ContentBlock anthropicResponseBlock `json:"content_block"`
}

// sseContentBlockDelta carries a text delta for the current content block.
type sseContentBlockDelta struct {
	Type  string             `json:"type"`  // "content_block_delta"
	Index int                `json:"index"`
	Delta anthropicTextDelta `json:"delta"`
}

// anthropicTextDelta is the text delta within a content_block_delta event.
type anthropicTextDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text"`
}

// sseContentBlockStop signals the end of a content block.
type sseContentBlockStop struct {
	Type  string `json:"type"` // "content_block_stop"
	Index int    `json:"index"`
}

// sseMessageDelta is the penultimate event carrying stop_reason and usage.
type sseMessageDelta struct {
	Type  string              `json:"type"` // "message_delta"
	Delta anthropicStopDelta  `json:"delta"`
	Usage anthropicUsage      `json:"usage"`
}

// anthropicStopDelta is the delta portion of a message_delta event.
type anthropicStopDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// sseMessageStop is the terminal event before [DONE].
type sseMessageStop struct {
	Type string `json:"type"` // "message_stop"
}

// writeAnthropicSSEEvent writes a single Anthropic SSE event:
//
//	event: <eventType>
//	data: <json>
//
// It flushes if w implements http.Flusher.
func writeAnthropicSSEEvent(w io.Writer, eventType string, data interface{}) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sse anthropic: marshal: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonBytes); err != nil {
		return fmt.Errorf("sse anthropic: write: %w", err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// writeAnthropicSSEDone writes the terminal [DONE] marker and flushes.
func writeAnthropicSSEDone(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "data: [DONE]\n\n"); err != nil {
		return fmt.Errorf("sse anthropic: done: %w", err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

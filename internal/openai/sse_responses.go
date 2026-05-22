package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sseResponseEvent is a single SSE event for the Responses API streaming format.
type sseResponseEvent struct {
	Type        string             `json:"type"`
	Delta       string             `json:"delta,omitempty"`
	ItemID      string             `json:"item_id,omitempty"`
	OutputIndex int                `json:"output_index,omitempty"`
	ContentIdx  int                `json:"content_index,omitempty"`
	Response    *responsesResponse `json:"response,omitempty"`
}

// writeSSEResponseEvent writes a single SSE data event for a Responses API streaming event.
// It marshals the event to JSON, writes "data: <json>\n\n", and flushes.
func writeSSEResponseEvent(w io.Writer, event sseResponseEvent) error {
	jsonBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("sse response: marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonBytes); err != nil {
		return fmt.Errorf("sse response: write: %w", err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

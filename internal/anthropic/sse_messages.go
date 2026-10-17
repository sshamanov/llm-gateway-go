package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sseMessageStart is the first event in an Anthropic streaming response. It
// carries the message envelope with empty content.
type sseMessageStart struct {
	Type    string                   `json:"type"` // "message_start"
	Message anthropicStreamEnvelope `json:"message"`
}

// anthropicStreamEnvelope is the message object inside message_start. Unlike
// the non-streaming response, stop_reason is explicitly null.
type anthropicStreamEnvelope struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"` // "message"
	Role         string                   `json:"role"` // "assistant"
	Content      []anthropicResponseBlock `json:"content"`
	Model        string                   `json:"model"`
	StopReason   *string                  `json:"stop_reason"`
	StopSequence *string                  `json:"stop_sequence"`
	Usage        anthropicUsage           `json:"usage"`
}

// sseContentBlockStart opens a content block. ContentBlock is a raw object so
// a text block can carry an explicit empty "text" and a tool_use block an
// empty "input", which SDK accumulators require.
type sseContentBlockStart struct {
	Type         string          `json:"type"` // "content_block_start"
	Index        int             `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
}

// textBlockStart is the content_block for a text block start.
type textBlockStart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// toolUseBlockStart is the content_block for a tool_use block start; the
// arguments follow as input_json_delta.
type toolUseBlockStart struct {
	Type  string          `json:"type"` // "tool_use"
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// sseContentBlockDelta carries a delta for the current content block. Delta is a
// raw JSON object — either a text_delta or an input_json_delta for tool_use.
type sseContentBlockDelta struct {
	Type  string          `json:"type"` // "content_block_delta"
	Index int             `json:"index"`
	Delta json.RawMessage `json:"delta"`
}

// anthropicTextDelta is the text delta within a content_block_delta event.
type anthropicTextDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text"`
}

// anthropicToolUseDelta is the delta for tool_use blocks; partial_json carries
// the JSON string of the tool input.
type anthropicToolUseDelta struct {
	Type        string `json:"type"` // "input_json_delta"
	PartialJSON string `json:"partial_json"`
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

// sseMessageStop is the terminal event of a stream.
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

// mustRaw marshals v for embedding as a json.RawMessage. The inputs are
// fixed local structs, so marshalling cannot fail in practice.
func mustRaw(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

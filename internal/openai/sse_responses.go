package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-go-proxy/internal/ollama"
)

// writeSSEResponseEvent writes one Responses API SSE event as
// "event: <type>\ndata: <json>\n\n" and flushes.
func writeSSEResponseEvent(w io.Writer, event map[string]interface{}) error {
	jsonBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("sse response: marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], jsonBytes); err != nil {
		return fmt.Errorf("sse response: write: %w", err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// responsesStream emits the Responses API streaming event sequence:
//
//	response.created, response.in_progress,
//	per message item: output_item.added, content_part.added,
//	  output_text.delta..., output_text.done, content_part.done, output_item.done
//	per function call: output_item.added, function_call_arguments.delta,
//	  function_call_arguments.done, output_item.done
//	response.completed (or response.failed)
//
// Every event carries a monotonically increasing sequence_number.
type responsesStream struct {
	w       io.Writer
	id      string
	model   string
	created int64
	seq     int
	err     error

	output []responsesOutput // finished items, in output_index order

	textOpen  bool
	textIndex int
	textID    string
	text      strings.Builder
}

func newResponsesStream(w io.Writer, id, model string) *responsesStream {
	return &responsesStream{w: w, id: id, model: model, created: time.Now().Unix()}
}

// emit writes one event; after the first write error all later emits are
// no-ops and Err reports it.
func (s *responsesStream) emit(eventType string, fields map[string]interface{}) {
	if s.err != nil {
		return
	}
	fields["type"] = eventType
	fields["sequence_number"] = s.seq
	s.seq++
	s.err = writeSSEResponseEvent(s.w, fields)
}

// Err returns the first write error (typically a client disconnect).
func (s *responsesStream) Err() error { return s.err }

func (s *responsesStream) envelope(status string, usage *responsesUsage) map[string]interface{} {
	output := s.output
	if output == nil {
		output = []responsesOutput{}
	}
	env := map[string]interface{}{
		"id":         s.id,
		"object":     "response",
		"created_at": s.created,
		"status":     status,
		"model":      s.model,
		"output":     output,
	}
	if usage != nil {
		env["usage"] = usage
	}
	return env
}

// Start emits response.created and response.in_progress.
func (s *responsesStream) Start() {
	s.emit("response.created", map[string]interface{}{"response": s.envelope("in_progress", nil)})
	s.emit("response.in_progress", map[string]interface{}{"response": s.envelope("in_progress", nil)})
}

// Text appends a text delta, opening the message item on first use.
func (s *responsesStream) Text(delta string) {
	if delta == "" {
		return
	}
	if !s.textOpen {
		s.textOpen = true
		s.textIndex = len(s.output)
		s.textID = fmt.Sprintf("msg_%s_%d", strings.TrimPrefix(s.id, "resp_"), s.textIndex)
		s.emit("response.output_item.added", map[string]interface{}{
			"output_index": s.textIndex,
			"item": map[string]interface{}{
				"type": "message", "id": s.textID, "status": "in_progress",
				"role": "assistant", "content": []interface{}{},
			},
		})
		s.emit("response.content_part.added", map[string]interface{}{
			"item_id": s.textID, "output_index": s.textIndex, "content_index": 0,
			"part": responsesOutputContent{Type: "output_text", Text: "", Annotations: []string{}},
		})
	}
	s.text.WriteString(delta)
	s.emit("response.output_text.delta", map[string]interface{}{
		"item_id": s.textID, "output_index": s.textIndex, "content_index": 0, "delta": delta,
	})
}

// closeText finishes the open message item, if any.
func (s *responsesStream) closeText() {
	if !s.textOpen {
		return
	}
	s.textOpen = false
	text := s.text.String()
	s.text.Reset()
	part := responsesOutputContent{Type: "output_text", Text: text, Annotations: []string{}}
	s.emit("response.output_text.done", map[string]interface{}{
		"item_id": s.textID, "output_index": s.textIndex, "content_index": 0, "text": text,
	})
	s.emit("response.content_part.done", map[string]interface{}{
		"item_id": s.textID, "output_index": s.textIndex, "content_index": 0, "part": part,
	})
	item := responsesOutput{
		Type: "message", ID: s.textID, Role: "assistant", Status: "completed",
		Content: []responsesOutputContent{part},
	}
	s.emit("response.output_item.done", map[string]interface{}{"output_index": s.textIndex, "item": item})
	s.output = append(s.output, item)
}

// ToolCall emits a complete function_call item (Ollama sends each call whole).
func (s *responsesStream) ToolCall(tc ollama.ChatToolCall) {
	s.closeText()
	args := string(tc.Function.Arguments)
	if args == "" || args == "null" {
		args = "{}"
	}
	callID := newToolCallID()
	index := len(s.output)
	s.emit("response.output_item.added", map[string]interface{}{
		"output_index": index,
		"item": map[string]interface{}{
			"type": "function_call", "id": callID, "call_id": callID,
			"name": tc.Function.Name, "arguments": "", "status": "in_progress",
		},
	})
	s.emit("response.function_call_arguments.delta", map[string]interface{}{
		"item_id": callID, "output_index": index, "delta": args,
	})
	s.emit("response.function_call_arguments.done", map[string]interface{}{
		"item_id": callID, "output_index": index, "arguments": args,
	})
	item := responsesOutput{
		Type: "function_call", ID: callID, CallID: callID,
		Name: tc.Function.Name, Arguments: args, Status: "completed",
	}
	s.emit("response.output_item.done", map[string]interface{}{"output_index": index, "item": item})
	s.output = append(s.output, item)
}

// Complete closes any open item and emits response.completed with usage.
func (s *responsesStream) Complete(final *ollama.ChatResponse) {
	s.closeText()
	usage := responsesUsage{
		InputTokens:  final.PromptEvalCount,
		OutputTokens: final.EvalCount,
		TotalTokens:  final.PromptEvalCount + final.EvalCount,
	}
	s.emit("response.completed", map[string]interface{}{"response": s.envelope("completed", &usage)})
}

// Fail closes any open item and emits response.failed.
func (s *responsesStream) Fail(message string) {
	s.closeText()
	env := s.envelope("failed", nil)
	env["error"] = map[string]string{"code": "server_error", "message": message}
	s.emit("response.failed", map[string]interface{}{"response": env})
}

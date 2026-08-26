package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"llm-go-proxy/internal/ollama"
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
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []sseToolCall `json:"tool_calls,omitempty"`
}

// sseToolCall is a function invocation delta in a streaming chat chunk.
type sseToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function sseToolCallFunction `json:"function,omitempty"`
}

// sseToolCallFunction holds the name and stringified arguments in a tool delta.
type sseToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// sseUsage holds token usage statistics (only present in final chunk).
type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// writeSSEChatChunk writes a single SSE data event for a chat completion chunk.
// role is only non-empty for the first content/tool chunk.
// finishReason is only non-empty for the terminal chunk.
// usage is only non-nil for the terminal chunk.
// Returns any error from writing or flushing.
func writeSSEChatChunk(w io.Writer, id, model string, created int64, role, content string, toolCalls []sseToolCall, finishReason string, usage *sseUsage) error {
	chunk := sseChatChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []sseChatChoice{
			{
				Index: 0,
				Delta: sseChatDelta{
					Role:      role,
					Content:   content,
					ToolCalls: toolCalls,
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

// mapSSEToolCalls converts Ollama tool_calls to the OpenAI streaming delta
// shape: stringified arguments, generated id, type "function", and an index.
func mapSSEToolCalls(calls []ollama.ChatToolCall) []sseToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]sseToolCall, 0, len(calls))
	for i, c := range calls {
		args := string(c.Function.Arguments)
		if args == "" || args == "null" {
			args = "{}"
		}
		out = append(out, sseToolCall{
			Index: i,
			ID:    newToolCallID(),
			Type:  "function",
			Function: sseToolCallFunction{
				Name:      c.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

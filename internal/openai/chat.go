package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
	"llm-go-proxy/internal/scheduler"
)

// maxRequestBodySize limits the incoming request body to 10 MB.
const maxRequestBodySize = 10 * 1024 * 1024

// chatCompletionRequest is the incoming OpenAI-compatible chat completions request body.
type chatCompletionRequest struct {
	Model       string               `json:"model"`
	Messages    []chatRequestMessage  `json:"messages"`
	Stream      *bool                `json:"stream,omitempty"`
	MaxTokens   int                  `json:"max_tokens,omitempty"`
	Temperature *float64             `json:"temperature,omitempty"`
	TopP        *float64             `json:"top_p,omitempty"`
	Stop        json.RawMessage      `json:"stop,omitempty"`
	Tools       json.RawMessage      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage      `json:"tool_choice,omitempty"`
}

// chatRequestMessage is a single message in the incoming request.
type chatRequestMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// openAIToolCall is a function invocation in an OpenAI assistant message.
type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

// openAIToolCallFunction holds the function name and stringified arguments.
type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openaiContentPart represents a single block in an array-typed message content.
// Used for multimodal messages where content is an array like:
//
//	[{"type":"text","text":"..."}, {"type":"image_url","image_url":{"url":"data:..."}}]
type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

// openaiImageURL holds the image URL in a content part.
type openaiImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// chatCompletionResponse is the OpenAI-compatible chat completions response body.
type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

// chatChoice is a single completion choice in the response.
type chatChoice struct {
	Index        int             `json:"index"`
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

// responseMessage is the assistant message in a completion choice.
type responseMessage struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	ToolCalls []responseToolCall `json:"tool_calls,omitempty"`
}

// responseToolCall is a function invocation in a chat completion response.
type responseToolCall struct {
	ID       string                   `json:"id"`
	Type     string                   `json:"type"`
	Function responseToolCallFunction `json:"function"`
}

// responseToolCallFunction holds the name and stringified arguments.
type responseToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatUsage holds token usage statistics.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatError is the OpenAI-compatible error response body.
type chatError struct {
	Error chatErrorDetail `json:"error"`
}

// chatErrorDetail holds the details of an error.
type chatErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ChatCompletionsHandler returns an HTTP handler for the OpenAI-compatible
// POST /v1/chat/completions endpoint.
func ChatCompletionsHandler(logger *logging.Logger, registry *backend.Registry, sched *scheduler.Scheduler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Read body (limited to maxRequestBodySize).
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Failed to read request body", "invalid_request_error", "")
			return
		}

		// Step 2: JSON decode into chatCompletionRequest.
		var chatReq chatCompletionRequest
		if err := json.Unmarshal(body, &chatReq); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error(), "invalid_request_error", "")
			return
		}

		// Step 4: If Model is empty, reject.
		if chatReq.Model == "" {
			writeJSONError(w, http.StatusBadRequest, "model is required", "invalid_request_error", "")
			return
		}

		// Step 5: Resolve model.
		resolved, err := registry.Resolve(chatReq.Model)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "Unknown model: "+chatReq.Model, "model_not_found", "")
			return
		}

		// Step 6: Convert messages.
		messages, err := convertOpenAIChatMessages(chatReq.Messages)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid messages: "+err.Error(), "invalid_request_error", "")
			return
		}

		// Step 7: Build merged options using the first candidate.
		ollamaReq := buildOllamaRequest(
			resolved.Candidates[0],
			messages,
			registry.OllamaDefaults(),
			resolved.AliasConfig,
			&chatReq,
		)

		// Step 7b: If streaming, handle via streaming path.
		if chatReq.Stream != nil && *chatReq.Stream {
			handleStreamChatCompletion(w, r, logger, sched, chatReq.Model, resolved.Candidates, resolved.AliasConfig, messages, ollamaReq.KeepAlive, ollamaReq.Think, ollamaReq.Options, ollamaReq.Tools)
			return
		}

		// Step 8: Create job ID.
		jobID, err := scheduler.NewJobID()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to generate job ID", "server_error", "")
			return
		}

		// Step 9: Create and submit job to scheduler.
		jobCtx, cancelJob := context.WithCancel(r.Context())
		defer cancelJob()

		kind := chatJobKind(ollamaReq.Tools)
		job := &scheduler.Job{
			ID:             jobID,
			Kind:           kind,
			Priority:       kind.Priority(),
			CreatedAt:      time.Now(),
			RequestedModel: chatReq.Model,
			Candidates:     resolved.Candidates,
			AliasConfig:    resolved.AliasConfig,
			Messages:       messages,
			KeepAlive:      ollamaReq.KeepAlive,
			Think:          ollamaReq.Think,
			Options:        ollamaReq.Options,
			Tools:          ollamaReq.Tools,
			ResultChan:     make(chan scheduler.JobResult, 1),
			JobCtx:         jobCtx,
		}

		if err := sched.Submit(job); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "Queue full: "+err.Error(), "server_error", "queue_full")
			return
		}

		// Step 10: Wait for result or client disconnect.
		var result scheduler.JobResult
		select {
		case result = <-job.ResultChan:
		case <-r.Context().Done():
			logger.Warn("chat client disconnected",
				logging.String("model", chatReq.Model),
			)
			return
		}
		if result.Err != nil {
			logger.Error("chat job failed",
				logging.String("model", chatReq.Model),
				logging.String("error", result.Err.Error()),
			)
			writeJSONError(w, http.StatusBadGateway, "Backend error: "+result.Err.Error(), "server_error", "")
			return
		}

		// Step 11: Map response to OpenAI-compatible shape.
		response := mapChatResponse(result.Response, chatReq.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode chat response",
				logging.String("error", err.Error()),
			)
		}
	})
}

// mapChatResponse converts an Ollama ChatResponse to an OpenAI-compatible
// chatCompletionResponse.
func mapChatResponse(ollamaResp *ollama.ChatResponse, requestedModel string) chatCompletionResponse {
	message := responseMessage{
		Role:    "assistant",
		Content: ollamaResp.Message.Content,
	}
	finishReason := mapFinishReason(ollamaResp.DoneReason)
	if toolCalls := mapToolCalls(ollamaResp.Message.ToolCalls); len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
		finishReason = "tool_calls"
	}

	return chatCompletionResponse{
		ID:     generateChatID(),
		Object: "chat.completion",
		Created: time.Now().Unix(),
		Model:  requestedModel,
		Choices: []chatChoice{
			{
				Index:       0,
				Message:     message,
				FinishReason: finishReason,
			},
		},
		Usage: chatUsage{
			PromptTokens:     ollamaResp.PromptEvalCount,
			CompletionTokens: ollamaResp.EvalCount,
			TotalTokens:      ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
		},
	}
}

// newToolCallID creates a unique OpenAI-style tool call id using crypto/rand.
func newToolCallID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("call_%016x", time.Now().UnixNano())
	}
	return "call_" + hex.EncodeToString(b)
}

// mapToolCalls converts Ollama tool_calls to the OpenAI shape: arguments (an
// object) is stringified and an id + type are added.
func mapToolCalls(calls []ollama.ChatToolCall) []responseToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]responseToolCall, 0, len(calls))
	for _, c := range calls {
		args := string(c.Function.Arguments)
		if args == "" || args == "null" {
			args = "{}"
		}
		out = append(out, responseToolCall{
			ID:   newToolCallID(),
			Type: "function",
			Function: responseToolCallFunction{
				Name:      c.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// mapFinishReason maps Ollama done_reason values to OpenAI finish_reason values.
func mapFinishReason(reason string) string {
	switch reason {
	case "stop", "length":
		return reason
	default:
		return "stop"
	}
}

// generateChatID creates a unique chat completion ID using crypto/rand.
func generateChatID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen on Linux.
		return fmt.Sprintf("chatcmpl-%016x", time.Now().UnixNano())
	}
	return "chatcmpl-" + hex.EncodeToString(b)
}

// writeJSONError writes an OpenAI-compatible JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(chatError{
		Error: chatErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	})
}

// convertOpenAIChatMessages converts OpenAI-format chat messages to Ollama format.
// Each message's content may be a plain string or an array of content parts
// (multimodal / vision requests).
func convertOpenAIChatMessages(msgs []chatRequestMessage) ([]ollama.ChatMessage, error) {
	result := make([]ollama.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		toolCalls := convertOpenAIToolCalls(msg.ToolCalls)
		var contentStr string
		if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
			result = append(result, ollama.ChatMessage{
				Role:      msg.Role,
				Content:   contentStr,
				ToolCalls: toolCalls,
			})
			continue
		}
		var parts []openaiContentPart
		if err := json.Unmarshal(msg.Content, &parts); err != nil {
			return nil, fmt.Errorf("convert message: %w", err)
		}
		text, images, err := convertContentParts(parts)
		if err != nil {
			return nil, fmt.Errorf("convert message: %w", err)
		}
		result = append(result, ollama.ChatMessage{
			Role:      msg.Role,
			Content:   text,
			Images:    images,
			ToolCalls: toolCalls,
		})
	}
	return result, nil
}

// convertOpenAIToolCalls converts OpenAI tool_calls to the Ollama shape:
// arguments (a JSON string in OpenAI) is kept as a JSON object and the id/type
// fields are dropped, since Ollama does not carry them.
func convertOpenAIToolCalls(calls []openAIToolCall) []ollama.ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ollama.ChatToolCall, 0, len(calls))
	for _, c := range calls {
		args := json.RawMessage(`{}`)
		if raw := []byte(c.Function.Arguments); len(raw) > 0 && raw[0] == '{' && json.Valid(raw) {
			args = raw
		}
		out = append(out, ollama.ChatToolCall{
			Function: ollama.ChatToolCallFunction{
				Name:      c.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// convertContentParts extracts text and images from an array of content parts.
// Data URLs are stripped to raw base64 since Ollama expects raw base64 in the images field.
func convertContentParts(parts []openaiContentPart) (text string, images []string, err error) {
	for _, part := range parts {
		switch part.Type {
		case "text":
			text += part.Text
		case "image_url":
			if part.ImageURL != nil && part.ImageURL.URL != "" {
				images = append(images, stripDataURLPrefix(part.ImageURL.URL))
			}
		}
	}
	return text, images, nil
}

// stripDataURLPrefix strips the "data:<mediatype>;base64," prefix from a data URL,
// returning just the raw base64 content. Returns the original string if it doesn't
// match the data URL pattern.
func stripDataURLPrefix(url string) string {
	const prefix = ";base64,"
	idx := strings.Index(url, prefix)
	if idx == -1 {
		return url
	}
	return url[idx+len(prefix):]
}

// buildOllamaRequest assembles an Ollama ChatRequest by merging settings in order:
//  1. Global ollama_defaults
//  2. Client overrides (harmless options always forwarded: max_tokens, temperature, top_p, stop)
//  3. Alias overrides (wins over client when set)
func buildOllamaRequest(
	modelName string,
	messages []ollama.ChatMessage,
	defaults config.OllamaDefaultsConfig,
	alias *config.AliasConfig,
	req *chatCompletionRequest,
) ollama.ChatRequest {
	// Step 1: Start from global defaults (keep_alive, think only).
	result := ollama.ChatRequest{
		Model:     modelName,
		Messages:  messages,
		Stream:    false,
		KeepAlive: defaults.KeepAlive,
		Think:     defaults.Think,
		Tools:     req.Tools,
	}

	// Step 2: Client overrides (always forwarded — harmless per-request params).
	if req.MaxTokens > 0 {
		if result.Options == nil {
			result.Options = &ollama.ChatOptions{}
		}
		result.Options.NumPredict = req.MaxTokens
	}
	if req.Temperature != nil {
		if result.Options == nil {
			result.Options = &ollama.ChatOptions{}
		}
		result.Options.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		if result.Options == nil {
			result.Options = &ollama.ChatOptions{}
		}
		result.Options.TopP = *req.TopP
	}
	if req.Stop != nil {
		stops := parseStopField(req.Stop)
		if len(stops) > 0 {
			if result.Options == nil {
				result.Options = &ollama.ChatOptions{}
			}
			result.Options.Stop = stops
		}
	}

	// Step 3: Alias overrides (wins over client when explicitly set).
	if alias != nil {
		if alias.Overrides.Think != nil {
			result.Think = alias.Overrides.Think
		}
		if alias.Overrides.KeepAlive != "" {
			result.KeepAlive = alias.Overrides.KeepAlive
		}
		if alias.Overrides.Options != nil {
			opts := alias.Overrides.Options
			if opts.Temperature != 0 || opts.TopP != 0 || opts.TopK != 0 ||
				opts.RepeatPenalty != 0 || opts.NumPredict != 0 || opts.NumCtx != 0 ||
				opts.UseMmap != nil {
				aliasOpts := &ollama.ChatOptions{
					Temperature:   opts.Temperature,
					TopP:          opts.TopP,
					TopK:          opts.TopK,
					RepeatPenalty: opts.RepeatPenalty,
					NumPredict:    opts.NumPredict,
					NumCtx:        opts.NumCtx,
					UseMmap:       opts.UseMmap,
				}
				// Merge: alias overwrites individual fields that are explicitly set,
				// preserving fields set by client that alias didn't touch.
				if result.Options == nil {
					result.Options = aliasOpts
				} else {
					if opts.Temperature != 0 {
						result.Options.Temperature = aliasOpts.Temperature
					}
					if opts.TopP != 0 {
						result.Options.TopP = aliasOpts.TopP
					}
					if opts.TopK != 0 {
						result.Options.TopK = aliasOpts.TopK
					}
					if opts.RepeatPenalty != 0 {
						result.Options.RepeatPenalty = aliasOpts.RepeatPenalty
					}
					if opts.NumPredict != 0 {
						result.Options.NumPredict = aliasOpts.NumPredict
					}
					if opts.NumCtx != 0 {
						result.Options.NumCtx = aliasOpts.NumCtx
					}
					if opts.UseMmap != nil {
						result.Options.UseMmap = aliasOpts.UseMmap
					}
				}
			}
		}
	}

	return result
}

// parseStopField attempts to parse a JSON stop field as either a string array
// or a single string value.
func parseStopField(raw json.RawMessage) []string {
	// Try JSON array of strings.
	var stops []string
	if err := json.Unmarshal(raw, &stops); err == nil {
		return stops
	}
	// Try single string.
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	return nil
}

// hasTools reports whether a raw tools field carries any tool definitions.
func hasTools(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("[]"))
}

// chatJobKind returns the scheduler job kind for a chat request: tool requests
// are prioritized as KindTool when the request carries tool definitions.
func chatJobKind(tools json.RawMessage) scheduler.JobKind {
	if hasTools(tools) {
		return scheduler.KindTool
	}
	return scheduler.KindChat
}

// handleStreamChatCompletion processes a streaming chat completion request.
// It creates a streaming Job, submits it to the scheduler, reads streaming
// chunks from StreamCh, and writes SSE events to the response writer.
func handleStreamChatCompletion(
	w http.ResponseWriter,
	r *http.Request,
	logger *logging.Logger,
	sched *scheduler.Scheduler,
	requestedModel string,
	candidates []string,
	aliasConfig *config.AliasConfig,
	messages []ollama.ChatMessage,
	keepAlive string,
	think *bool,
	options *ollama.ChatOptions,
	tools json.RawMessage,
) {
	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Create a cancellable context derived from the request context.
	streamCtx, cancelStream := context.WithCancel(r.Context())
	defer cancelStream()

	// Create job ID.
	jobID, err := scheduler.NewJobID()
	if err != nil {
		// Can't write headers after status already sent, so just log.
		if logger != nil {
			logger.Error("streaming: failed to generate job ID",
				logging.String("error", err.Error()),
			)
		}
		return
	}

	// Create streaming job.
	streamCh := make(chan *ollama.StreamChunk, 20)
	kind := chatJobKind(tools)
	job := &scheduler.Job{
		ID:             jobID,
		Kind:           kind,
		Priority:       kind.Priority(),
		CreatedAt:      time.Now(),
		Streaming:      true,
		RequestedModel: requestedModel,
		Candidates:     candidates,
		AliasConfig:    aliasConfig,
		Messages:       messages,
		KeepAlive:      keepAlive,
		Think:          think,
		Options:        options,
		Tools:          tools,
		StreamCh:       streamCh,
		ResultChan:     make(chan scheduler.JobResult, 1),
		JobCtx:         streamCtx,
	}

	// Submit to scheduler.
	if err := sched.Submit(job); err != nil {
		// Write error as SSE event and stop.
		errJSON := fmt.Sprintf(`{"error":{"message":"Queue full: %s","type":"server_error","code":"queue_full"}}`, err.Error())
		fmt.Fprintf(w, "data: %s\n\n", errJSON)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	// Cancel the stream context when the client disconnects.
	go func() {
		select {
		case <-r.Context().Done():
			cancelStream()
		case <-streamCtx.Done():
			// Already cancelled by normal completion.
		}
	}()

	chatID := generateChatID()
	created := time.Now().Unix()
	firstChunk := true
	sawToolCalls := false

	// Read chunks from the scheduler's stream channel.
	for chunk := range job.StreamCh {
		if chunk == nil {
			continue
		}
		if chunk.Err != nil {
			if logger != nil {
				logger.Warn("streaming chunk error",
					logging.String("job_id", jobID),
					logging.String("error", chunk.Err.Error()),
				)
			}
			break
		}
		if chunk.Response == nil {
			continue
		}

		resp := chunk.Response

		// Determine delta content.
		role := ""
		content := resp.Message.Content
		toolCalls := mapSSEToolCalls(resp.Message.ToolCalls)
		if len(toolCalls) > 0 {
			sawToolCalls = true
		}

		// First chunk with content or tool calls gets the role delta too.
		if firstChunk && (content != "" || len(toolCalls) > 0) {
			role = "assistant"
			firstChunk = false
		}

		// Build finish reason and usage for final chunk. Ollama sends tool calls
		// in their own chunk, so track them across the stream for the final
		// finish_reason.
		finishReason := ""
		var usage *sseUsage
		if resp.Done {
			finishReason = mapFinishReason(resp.DoneReason)
			if sawToolCalls {
				finishReason = "tool_calls"
			}
			usage = &sseUsage{
				PromptTokens:     resp.PromptEvalCount,
				CompletionTokens: resp.EvalCount,
				TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
			}
		}

		// Write SSE chunk.
		if err := writeSSEChatChunk(w, chatID, requestedModel, created, role, content, toolCalls, finishReason, usage); err != nil {
			// Client probably disconnected — cancel the backend.
			cancelStream()
			return
		}
	}

	// Write [DONE] marker.
	writeSSEDone(w)

	// Wait for final result (for logging purposes, non-blocking via select).
	select {
	case result := <-job.ResultChan:
		if result.Err != nil && logger != nil {
			logger.Warn("streaming job finished with error",
				logging.String("job_id", jobID),
				logging.String("error", result.Err.Error()),
			)
		}
	default:
		// Result may already have been consumed or not sent.
	}
}

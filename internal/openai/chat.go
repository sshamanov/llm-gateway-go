package openai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Role    string `json:"role"`
	Content string `json:"content"`
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
		messages := make([]ollama.ChatMessage, len(chatReq.Messages))
		for i, msg := range chatReq.Messages {
			messages[i] = ollama.ChatMessage{Role: msg.Role, Content: msg.Content}
		}

		// Step 7: Build merged options using the first candidate.
		ollamaReq := buildOllamaRequest(
			resolved.Candidates[0],
			messages,
			registry.OllamaDefaults(),
			resolved.AliasConfig,
			&chatReq,
			registry.Policy(),
		)

		// Step 7b: If streaming, handle via streaming path.
		if chatReq.Stream != nil && *chatReq.Stream {
			handleStreamChatCompletion(w, r, logger, sched, chatReq.Model, resolved.Candidates, resolved.AliasConfig, messages, ollamaReq.Options)
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

		job := &scheduler.Job{
			ID:             jobID,
			Kind:           scheduler.KindChat,
			Priority:       scheduler.KindChat.Priority(),
			RequestedModel: chatReq.Model,
			Candidates:     resolved.Candidates,
			AliasConfig:    resolved.AliasConfig,
			Messages:       messages,
			Options:        ollamaReq.Options,
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
	finishReason := mapFinishReason(ollamaResp.DoneReason)

	return chatCompletionResponse{
		ID:     generateChatID(),
		Object: "chat.completion",
		Created: time.Now().Unix(),
		Model:  requestedModel,
		Choices: []chatChoice{
			{
				Index: 0,
				Message: responseMessage{
					Role:    "assistant",
					Content: ollamaResp.Message.Content,
				},
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

// buildOllamaRequest assembles an Ollama ChatRequest by merging settings in order:
//  1. Global ollama_defaults
//  2. Alias overrides (if the request used an alias)
//  3. Client overrides (only if allow_client_override_options is true)
func buildOllamaRequest(
	modelName string,
	messages []ollama.ChatMessage,
	defaults config.OllamaDefaultsConfig,
	alias *config.AliasConfig,
	req *chatCompletionRequest,
	policy config.PolicyConfig,
) ollama.ChatRequest {
	// Step 1: Start from defaults.
	thinkVal := defaults.Think
	result := ollama.ChatRequest{
		Model:     modelName,
		Messages:  messages,
		Stream:    false,
		KeepAlive: defaults.KeepAlive,
		Think:     &thinkVal,
		Options: &ollama.ChatOptions{
			NumThread:   defaults.Options.NumThread,
			NumCtx:      defaults.Options.NumCtx,
			Temperature: defaults.Options.Temperature,
			TopP:        defaults.Options.TopP,
		},
	}

	// Step 2: Alias overrides.
	if alias != nil {
		if alias.Overrides.Think != nil {
			result.Think = alias.Overrides.Think
		}
		if alias.Overrides.Options != nil {
			opts := alias.Overrides.Options
			if opts.NumThread != 0 {
				result.Options.NumThread = opts.NumThread
			}
			if opts.NumCtx != 0 {
				result.Options.NumCtx = opts.NumCtx
			}
			if opts.Temperature != 0 {
				result.Options.Temperature = opts.Temperature
			}
			if opts.TopP != 0 {
				result.Options.TopP = opts.TopP
			}
		}
	}

	// Step 3: Client overrides (only if allowed by policy).
	if policy.AllowClientOverrideOptions {
		if req.MaxTokens > 0 {
			result.Options.NumPredict = req.MaxTokens
		}
		if req.Temperature != nil {
			result.Options.Temperature = *req.Temperature
		}
		if req.TopP != nil {
			result.Options.TopP = *req.TopP
		}
		if req.Stop != nil {
			stops := parseStopField(req.Stop)
			if len(stops) > 0 {
				result.Options.Stop = stops
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
	options *ollama.ChatOptions,
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
	job := &scheduler.Job{
		ID:             jobID,
		Kind:           scheduler.KindChat,
		Priority:       scheduler.KindChat.Priority(),
		Streaming:      true,
		RequestedModel: requestedModel,
		Candidates:     candidates,
		AliasConfig:    aliasConfig,
		Messages:       messages,
		Options:        options,
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

		// First chunk with content gets the role delta too.
		if firstChunk && content != "" {
			role = "assistant"
			firstChunk = false
		}

		// Build finish reason and usage for final chunk.
		finishReason := ""
		var usage *sseUsage
		if resp.Done {
			finishReason = mapFinishReason(resp.DoneReason)
			usage = &sseUsage{
				PromptTokens:     resp.PromptEvalCount,
				CompletionTokens: resp.EvalCount,
				TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
			}
		}

		// Write SSE chunk.
		if err := writeSSEChatChunk(w, chatID, requestedModel, created, role, content, finishReason, usage); err != nil {
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

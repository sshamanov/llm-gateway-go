package anthropic

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

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

type anthropicMessageRequest struct {
	Model         string                    `json:"model"`
	Messages      []anthropicInputMessage   `json:"messages"`
	System        json.RawMessage           `json:"system,omitempty"`
	MaxTokens     int                       `json:"max_tokens"`
	Stream        *bool                     `json:"stream,omitempty"`
	Temperature   *float64                  `json:"temperature,omitempty"`
	TopP          *float64                  `json:"top_p,omitempty"`
	TopK          *int                      `json:"top_k,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
	Tools         json.RawMessage           `json:"tools,omitempty"`
	ToolChoice    json.RawMessage           `json:"tool_choice,omitempty"`
	Thinking      *anthropicThinkingConfig  `json:"thinking,omitempty"`
}

type anthropicInputMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicContentBlockSource struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     json.RawMessage       `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   json.RawMessage       `json:"content,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded image
}

type anthropicThinkingConfig struct {
	Type         string `json:"type"` // "enabled" or "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

type anthropicMessageResponse struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"` // "message"
	Role         string                   `json:"role"` // "assistant"
	Content      []anthropicResponseBlock `json:"content"`
	Model        string                   `json:"model"`
	StopReason   string                   `json:"stop_reason"`
	StopSequence *string                  `json:"stop_sequence,omitempty"`
	Usage        anthropicUsage           `json:"usage"`
}

type anthropicResponseBlock struct {
	Type     string          `json:"type"` // "text" or "thinking"
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

type anthropicErrorResponse struct {
	Type  string               `json:"type"` // "error"
	Error anthropicErrorDetail `json:"error"`
}

type anthropicErrorDetail struct {
	Type    string `json:"type"`    // "invalid_request_error", "api_error"
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Content conversion functions
// ---------------------------------------------------------------------------

// convertInputMessages converts Anthropic input messages to Ollama ChatMessage
// slice. For each message: if content is a string, use it directly. If content
// is an array of content blocks, extract text from text blocks and images from
// image blocks.
func convertInputMessages(msgs []anthropicInputMessage) ([]ollama.ChatMessage, error) {
	result := make([]ollama.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		// Try unmarshal as string first.
		var contentStr string
		if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
			result = append(result, ollama.ChatMessage{
				Role:    msg.Role,
				Content: contentStr,
			})
			continue
		}

		// Otherwise, try as content blocks array.
		var blocks []anthropicContentBlockSource
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			return nil, fmt.Errorf("convert input message: %w", err)
		}

		text, images, err := convertContentBlocks(blocks)
		if err != nil {
			return nil, fmt.Errorf("convert input message: %w", err)
		}

		result = append(result, ollama.ChatMessage{
			Role:    msg.Role,
			Content: text,
			Images:  images,
		})
	}
	return result, nil
}

// convertSystem converts the Anthropic system field (string or array of text
// blocks) to a system message. Returns nil if system is empty/omitted.
func convertSystem(raw json.RawMessage) (*ollama.ChatMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// Try string.
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if str == "" {
			return nil, nil
		}
		return &ollama.ChatMessage{Role: "system", Content: str}, nil
	}

	// Try array of text blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("convert system: expected string or array of text blocks: %w", err)
	}

	var combined string
	for _, b := range blocks {
		if b.Type == "text" {
			combined += b.Text
		}
	}

	if combined == "" {
		return nil, nil
	}

	return &ollama.ChatMessage{Role: "system", Content: combined}, nil
}

// convertContentBlocks processes an array of content blocks into combined text
// and images.
func convertContentBlocks(blocks []anthropicContentBlockSource) (text string, images []string, err error) {
	for _, block := range blocks {
		switch block.Type {
		case "text":
			text += block.Text
		case "image":
			if block.Source != nil {
				images = append(images, fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data))
			}
		default:
			// For tool_use, tool_result, or any other block type: serialize to JSON
			// and append to the text content.
			blockJSON, marshalErr := json.Marshal(block)
			if marshalErr != nil {
				return text, images, fmt.Errorf("convert content block %q: %w", block.Type, marshalErr)
			}
			if text != "" {
				text += "\n"
			}
			text += string(blockJSON)
		}
	}
	return text, images, nil
}

// ---------------------------------------------------------------------------
// Non-streaming handler
// ---------------------------------------------------------------------------

// MessagesHandler returns an HTTP handler for the Anthropic-compatible
// POST /v1/messages endpoint.
func MessagesHandler(logger *logging.Logger, registry *backend.Registry, sched *scheduler.Scheduler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Read body (limited to maxRequestBodySize).
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}

		// Step 2: JSON decode into anthropicMessageRequest.
		var req anthropicMessageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
			return
		}

		// Step 3: If Model is empty, reject.
		if req.Model == "" {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}

		// Step 4: If MaxTokens is 0, reject.
		if req.MaxTokens == 0 {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens is required")
			return
		}

		// Step 5: Resolve model.
		resolved, err := registry.Resolve(req.Model)
		if err != nil {
			writeAnthropicError(w, http.StatusNotFound, "invalid_request_error", "Unknown model: "+req.Model)
			return
		}

		// Step 6: Convert system prompt.
		sysMsg, err := convertSystem(req.System)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid system field: "+err.Error())
			return
		}

		// Step 7: Convert messages.
		messages, err := convertInputMessages(req.Messages)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid messages: "+err.Error())
			return
		}

		// Prepend system message if present.
		if sysMsg != nil {
			messages = append([]ollama.ChatMessage{*sysMsg}, messages...)
		}

		// Step 8: Build merged options.
		options, think, keepAlive := buildAnthropicOptions(
			registry.OllamaDefaults(),
			resolved.AliasConfig,
			&req,
			registry.Policy(),
		)

		// Step 9: If streaming, branch to streaming handler.
		if req.Stream != nil && *req.Stream {
			handleStreamAnthropic(w, r, logger, sched, req.Model, resolved.Candidates, resolved.AliasConfig, messages, keepAlive, options, think)
			return
		}

		// Step 10: Create job ID.
		jobID, err := scheduler.NewJobID()
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Failed to generate job ID")
			return
		}

		// Step 11: Create and submit job to scheduler.
		jobCtx, cancelJob := context.WithCancel(r.Context())
		defer cancelJob()

		job := &scheduler.Job{
			ID:             jobID,
			Kind:           scheduler.KindChat,
			Priority:       scheduler.KindChat.Priority(),
			CreatedAt:      time.Now(),
			RequestedModel: req.Model,
			Candidates:     resolved.Candidates,
			AliasConfig:    resolved.AliasConfig,
			Messages:       messages,
			Options:        options,
			Think:          think,
				KeepAlive:      keepAlive,
			ResultChan:     make(chan scheduler.JobResult, 1),
			JobCtx:         jobCtx,
		}

		if err := sched.Submit(job); err != nil {
			writeAnthropicError(w, http.StatusServiceUnavailable, "api_error", "Queue full: "+err.Error())
			return
		}

		// Step 12: Wait for result or client disconnect.
		var result scheduler.JobResult
		select {
		case result = <-job.ResultChan:
		case <-r.Context().Done():
			return
		}
		if result.Err != nil {
			logger.Error("anthropic messages job failed",
				logging.String("model", req.Model),
				logging.String("error", result.Err.Error()),
			)
			writeAnthropicError(w, http.StatusBadGateway, "api_error", "Backend error: "+result.Err.Error())
			return
		}

		// Step 13: Map response to Anthropic-compatible shape.
		response := mapAnthropicResponse(result.Response, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode anthropic message response",
				logging.String("error", err.Error()),
			)
		}
	})
}

// ---------------------------------------------------------------------------
// Options builder
// ---------------------------------------------------------------------------

// buildAnthropicOptions merges settings in order:
//  1. Global ollama_defaults (keep_alive, think only)
//  2. Alias overrides (think, temperature, top_p)
//  3. req.MaxTokens → options.NumPredict (always applied)
//  4. Client overrides (only if allow_client_override_options is true)
//  5. Thinking override (if applicable)
func buildAnthropicOptions(
	defaults config.OllamaDefaultsConfig,
	alias *config.AliasConfig,
	req *anthropicMessageRequest,
	policy config.PolicyConfig,
) (options *ollama.ChatOptions, think *bool, keepAlive string) {
	keepAlive = defaults.KeepAlive
	think = defaults.Think

	// Step 1: Alias overrides (think, temperature, top_p).
	if alias != nil {
		if alias.Overrides.Think != nil {
			think = alias.Overrides.Think
		}
		if alias.Overrides.KeepAlive != "" {
			keepAlive = alias.Overrides.KeepAlive
		}
		if alias.Overrides.Options != nil {
			opts := alias.Overrides.Options
			if opts.Temperature != 0 || opts.TopP != 0 || opts.TopK != 0 ||
				opts.RepeatPenalty != 0 || opts.NumPredict != 0 || opts.NumCtx != 0 {
				options = &ollama.ChatOptions{
					Temperature:   opts.Temperature,
					TopP:          opts.TopP,
					TopK:          opts.TopK,
					RepeatPenalty: opts.RepeatPenalty,
					NumPredict:    opts.NumPredict,
					NumCtx:        opts.NumCtx,
				}
			}
		}
	}

	// Step 2: req.MaxTokens → options.NumPredict (always).
	if req.MaxTokens > 0 {
		if options == nil {
			options = &ollama.ChatOptions{}
		}
		options.NumPredict = req.MaxTokens
	}

	// Step 3: Client overrides (only if allowed by policy).
	if policy.AllowClientOverrideOptions {
		if req.Temperature != nil {
			if options == nil {
				options = &ollama.ChatOptions{}
			}
			options.Temperature = *req.Temperature
		}
		if req.TopP != nil {
			if options == nil {
				options = &ollama.ChatOptions{}
			}
			options.TopP = *req.TopP
		}
		if req.TopK != nil {
			if options == nil {
				options = &ollama.ChatOptions{}
			}
			options.TopK = *req.TopK
		}
		if len(req.StopSequences) > 0 {
			if options == nil {
				options = &ollama.ChatOptions{}
			}
			options.Stop = req.StopSequences
		}
	}

	// Step 4: Thinking override (client only, not from alias/defaults).
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		thinkBool := true
		think = &thinkBool
		if options == nil {
			options = &ollama.ChatOptions{}
		}
		options.Temperature = 1.0
	}

	return options, think, keepAlive
}

// ---------------------------------------------------------------------------
// Response mapper
// ---------------------------------------------------------------------------

// mapAnthropicResponse converts an Ollama ChatResponse to an
// anthropicMessageResponse.
func mapAnthropicResponse(ollamaResp *ollama.ChatResponse, requestedModel string) anthropicMessageResponse {
	return anthropicMessageResponse{
		ID:   generateAnthropicID(),
		Type: "message",
		Role: "assistant",
		Content: []anthropicResponseBlock{
			{
				Type: "text",
				Text: ollamaResp.Message.Content,
			},
		},
		Model:      requestedModel,
		StopReason: mapStopReason(ollamaResp.DoneReason),
		Usage: anthropicUsage{
			InputTokens:  ollamaResp.PromptEvalCount,
			OutputTokens: ollamaResp.EvalCount,
		},
	}
}

// ---------------------------------------------------------------------------
// Streaming handler
// ---------------------------------------------------------------------------

// handleStreamAnthropic processes a streaming Anthropic messages request. It
// creates a streaming Job, submits it to the scheduler, reads streaming chunks
// from StreamCh, and writes SSE events to the response writer.
func handleStreamAnthropic(
	w http.ResponseWriter,
	r *http.Request,
	logger *logging.Logger,
	sched *scheduler.Scheduler,
	requestedModel string,
	candidates []string,
	aliasConfig *config.AliasConfig,
	messages []ollama.ChatMessage,
	keepAlive string,
	options *ollama.ChatOptions,
	think *bool,
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
			CreatedAt:      time.Now(),
		Streaming:      true,
		RequestedModel: requestedModel,
		Candidates:     candidates,
		AliasConfig:    aliasConfig,
		Messages:       messages,
		Options:        options,
			KeepAlive:      keepAlive,
		Think:          think,
		StreamCh:       streamCh,
		ResultChan:     make(chan scheduler.JobResult, 1),
		JobCtx:         streamCtx,
	}

	// Submit to scheduler.
	if err := sched.Submit(job); err != nil {
		writeAnthropicSSEEvent(w, "error", anthropicErrorResponse{
			Type: "error",
			Error: anthropicErrorDetail{
				Type:    "api_error",
				Message: "Queue full: " + err.Error(),
			},
		})
		return
	}

	// Cancel the stream context when the client disconnects.
	go func() {
		select {
		case <-r.Context().Done():
			cancelStream()
		case <-streamCtx.Done():
		}
	}()

	sentStart := false

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
		content := resp.Message.Content

		// Emit content block events for non-empty content.
		if content != "" {
			if !sentStart {
				// First content chunk: emit content_block_start (empty text) then
				// content_block_delta with the actual text.
				writeAnthropicSSEEvent(w, "content_block_start", sseContentBlockStart{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicResponseBlock{
						Type: "text",
						Text: "",
					},
				})
				sentStart = true

				writeAnthropicSSEEvent(w, "content_block_delta", sseContentBlockDelta{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicTextDelta{
						Type: "text_delta",
						Text: content,
					},
				})
			} else {
				writeAnthropicSSEEvent(w, "content_block_delta", sseContentBlockDelta{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicTextDelta{
						Type: "text_delta",
						Text: content,
					},
				})
			}
		}

		// Handle done events.
		if resp.Done {
			if sentStart {
				writeAnthropicSSEEvent(w, "content_block_stop", sseContentBlockStop{
					Type:  "content_block_stop",
					Index: 0,
				})
			}

			writeAnthropicSSEEvent(w, "message_delta", sseMessageDelta{
				Type: "message_delta",
				Delta: anthropicStopDelta{
					StopReason: mapStopReason(resp.DoneReason),
				},
				Usage: anthropicUsage{
					InputTokens:  resp.PromptEvalCount,
					OutputTokens: resp.EvalCount,
				},
			})

			writeAnthropicSSEEvent(w, "message_stop", sseMessageStop{
				Type: "message_stop",
			})
		}
	}

	// Write [DONE] marker.
	writeAnthropicSSEDone(w)

	// Non-blocking read from ResultChan for logging.
	select {
	case result := <-job.ResultChan:
		if result.Err != nil && logger != nil {
			logger.Warn("streaming job finished with error",
				logging.String("job_id", jobID),
				logging.String("error", result.Err.Error()),
			)
		}
	default:
	}
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// writeAnthropicError writes an Anthropic-compatible JSON error response.
func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(anthropicErrorResponse{
		Type: "error",
		Error: anthropicErrorDetail{
			Type:    errType,
			Message: message,
		},
	})
}

// generateAnthropicID creates a unique message ID using crypto/rand.
func generateAnthropicID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("msg_%016x", time.Now().UnixNano())
	}
	return "msg_" + hex.EncodeToString(b)
}

// mapStopReason maps Ollama done_reason values to Anthropic stop_reason values.
func mapStopReason(doneReason string) string {
	switch doneReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

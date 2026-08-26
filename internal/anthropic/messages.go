package anthropic

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

		text, images, toolCalls, hasToolResult, err := convertContentBlocks(blocks)
		if err != nil {
			return nil, fmt.Errorf("convert input message: %w", err)
		}

		switch {
		case len(toolCalls) > 0:
			// Assistant tool invocations become Ollama tool_calls on the message.
			result = append(result, ollama.ChatMessage{
				Role:      msg.Role,
				Content:   text,
				Images:    images,
				ToolCalls: toolCalls,
			})
		case hasToolResult:
			// Tool results map to Ollama's role "tool".
			result = append(result, ollama.ChatMessage{
				Role:    "tool",
				Content: text,
				Images:  images,
			})
		default:
			result = append(result, ollama.ChatMessage{
				Role:    msg.Role,
				Content: text,
				Images:  images,
			})
		}
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

// convertContentBlocks processes an array of content blocks into combined text,
// images, and tool calls. tool_use blocks become structured tool calls (Anthropic
// input object → Ollama arguments); the text content of tool_result blocks is
// folded into the returned text. hasToolResult reports whether any tool_result
// block was seen so the caller can emit a role:"tool" message.
func convertContentBlocks(blocks []anthropicContentBlockSource) (text string, images []string, toolCalls []ollama.ChatToolCall, hasToolResult bool, err error) {
	for _, block := range blocks {
		switch block.Type {
		case "text":
			text += block.Text
		case "image":
			if block.Source != nil {
				images = append(images, block.Source.Data)
			}
		case "tool_use":
			args := json.RawMessage(`{}`)
			if len(block.Input) > 0 && block.Input[0] == '{' && json.Valid(block.Input) {
				args = block.Input
			}
			toolCalls = append(toolCalls, ollama.ChatToolCall{
				Function: ollama.ChatToolCallFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			hasToolResult = true
			content, contentErr := flattenToolResult(block)
			if contentErr != nil {
				return text, images, toolCalls, hasToolResult, fmt.Errorf("convert content block %q: %w", block.Type, contentErr)
			}
			if text != "" {
				text += "\n"
			}
			text += content
		default:
			// Unknown block types: serialize to JSON as text fallback.
			blockJSON, marshalErr := json.Marshal(block)
			if marshalErr != nil {
				return text, images, toolCalls, hasToolResult, fmt.Errorf("convert content block %q: %w", block.Type, marshalErr)
			}
			if text != "" {
				text += "\n"
			}
			text += string(blockJSON)
		}
	}
	return text, images, toolCalls, hasToolResult, nil
}

// flattenToolResult extracts the text from a tool_result content field, which may
// be a plain string or an array of text/image blocks.
func flattenToolResult(block anthropicContentBlockSource) (string, error) {
	if len(block.Content) == 0 {
		return "", nil
	}
	// Plain string content.
	var str string
	if err := json.Unmarshal(block.Content, &str); err == nil {
		return str, nil
	}
	// Array of content blocks: keep only text.
	var parts []anthropicContentBlockSource
	if err := json.Unmarshal(block.Content, &parts); err != nil {
		// Non-string, non-block content: fall back to raw JSON.
		return string(block.Content), nil
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), nil
}

// convertAnthropicTools translates the Anthropic tool definition shape
// ([{name, description, input_schema}]) to the OpenAI/Ollama function shape
// ([{type:"function", function:{name, description, parameters}}]). Returns nil
// when raw is empty or null.
func convertAnthropicTools(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("convert tools: %w", err)
	}

	out := make([]ollamaFunctionTool, 0, len(tools))
	for _, t := range tools {
		params := t.InputSchema
		if len(params) == 0 || !json.Valid(params) {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, ollamaFunctionTool{
			Type: "function",
			Function: ollamaFunctionToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return json.Marshal(out)
}

// ollamaFunctionTool is the OpenAI/Ollama function-tool shape used for tool
// definitions forwarded to Ollama.
type ollamaFunctionTool struct {
	Type     string                 `json:"type"`
	Function ollamaFunctionToolFunc `json:"function"`
}

type ollamaFunctionToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// anthropicJobKind returns KindTool when the request carries tool definitions,
// KindChat otherwise (ARCHITECTURE §9.2).
func anthropicJobKind(tools json.RawMessage) scheduler.JobKind {
	if len(bytes.TrimSpace(tools)) > 0 {
		return scheduler.KindTool
	}
	return scheduler.KindChat
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

		// Step 8: Convert tool definitions to the Ollama shape.
		tools, err := convertAnthropicTools(req.Tools)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid tools: "+err.Error())
			return
		}

		// Step 9: Build merged options.
		options, think, keepAlive := buildAnthropicOptions(
			registry.OllamaDefaults(),
			resolved.AliasConfig,
			&req,
		)

		// Step 10: If streaming, branch to streaming handler.
		if req.Stream != nil && *req.Stream {
			handleStreamAnthropic(w, r, logger, sched, req.Model, resolved.Candidates, resolved.AliasConfig, messages, keepAlive, options, think, tools)
			return
		}

		// Step 11: Create job ID.
		jobID, err := scheduler.NewJobID()
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Failed to generate job ID")
			return
		}

		// Step 12: Create and submit job to scheduler.
		jobCtx, cancelJob := context.WithCancel(r.Context())
		defer cancelJob()

		job := &scheduler.Job{
			ID:             jobID,
			Kind:           anthropicJobKind(tools),
			Priority:       anthropicJobKind(tools).Priority(),
			CreatedAt:      time.Now(),
			RequestedModel: req.Model,
			Candidates:     resolved.Candidates,
			AliasConfig:    resolved.AliasConfig,
			Messages:       messages,
			Tools:          tools,
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

		// Step 13: Wait for result or client disconnect.
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

		// Step 14: Map response to Anthropic-compatible shape.
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

// buildAnthropicOptions merges settings in order (later steps override earlier):
//  1. Global ollama_defaults (keep_alive, think only)
//  2. Client overrides (always forwarded): max_tokens → num_predict, temperature,
//     top_p, top_k, stop_sequences
//  3. Alias overrides (wins over client when explicitly set)
//  4. Thinking override (Anthropic thinking.enabled forces temperature 1.0)
func buildAnthropicOptions(
	defaults config.OllamaDefaultsConfig,
	alias *config.AliasConfig,
	req *anthropicMessageRequest,
) (options *ollama.ChatOptions, think *bool, keepAlive string) {
	keepAlive = defaults.KeepAlive
	think = defaults.Think

	// Step 1: Client overrides (always forwarded — harmless per-request params).
	if req.MaxTokens > 0 {
		options = ensureChatOptions(options)
		options.NumPredict = req.MaxTokens
	}
	if req.Temperature != nil {
		options = ensureChatOptions(options)
		options.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		options = ensureChatOptions(options)
		options.TopP = *req.TopP
	}
	if req.TopK != nil {
		options = ensureChatOptions(options)
		options.TopK = *req.TopK
	}
	if len(req.StopSequences) > 0 {
		options = ensureChatOptions(options)
		options.Stop = req.StopSequences
	}

	// Step 2: Alias overrides (wins over client when explicitly set).
	if alias != nil {
		if alias.Overrides.Think != nil {
			think = alias.Overrides.Think
		}
		if alias.Overrides.KeepAlive != "" {
			keepAlive = alias.Overrides.KeepAlive
		}
		if alias.Overrides.Options != nil {
			opts := alias.Overrides.Options
			if opts.NumThread != 0 {
				options = ensureChatOptions(options)
				options.NumThread = opts.NumThread
			}
			if opts.NumCtx != 0 {
				options = ensureChatOptions(options)
				options.NumCtx = opts.NumCtx
			}
			if opts.Temperature != 0 {
				options = ensureChatOptions(options)
				options.Temperature = opts.Temperature
			}
			if opts.TopP != 0 {
				options = ensureChatOptions(options)
				options.TopP = opts.TopP
			}
			if opts.TopK != 0 {
				options = ensureChatOptions(options)
				options.TopK = opts.TopK
			}
			if opts.RepeatPenalty != 0 {
				options = ensureChatOptions(options)
				options.RepeatPenalty = opts.RepeatPenalty
			}
			if opts.NumPredict != 0 {
				options = ensureChatOptions(options)
				options.NumPredict = opts.NumPredict
			}
			if opts.UseMmap != nil {
				options = ensureChatOptions(options)
				options.UseMmap = opts.UseMmap
			}
		}
	}

	// Step 3: Thinking override (Anthropic thinking.enabled forces temperature 1.0).
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		thinkBool := true
		think = &thinkBool
		options = ensureChatOptions(options)
		options.Temperature = 1.0
	}

	return options, think, keepAlive
}

// ensureChatOptions returns o if non-nil, otherwise a new zero ChatOptions.
func ensureChatOptions(o *ollama.ChatOptions) *ollama.ChatOptions {
	if o == nil {
		return &ollama.ChatOptions{}
	}
	return o
}

// ---------------------------------------------------------------------------
// Response mapper
// ---------------------------------------------------------------------------

// mapAnthropicResponse converts an Ollama ChatResponse to an
// anthropicMessageResponse.
func mapAnthropicResponse(ollamaResp *ollama.ChatResponse, requestedModel string) anthropicMessageResponse {
	resp := anthropicMessageResponse{
		ID:         generateAnthropicID(),
		Type:       "message",
		Role:       "assistant",
		Model:      requestedModel,
		StopReason: mapStopReason(ollamaResp.DoneReason),
		Usage: anthropicUsage{
			InputTokens:  ollamaResp.PromptEvalCount,
			OutputTokens: ollamaResp.EvalCount,
		},
	}

	if len(ollamaResp.Message.ToolCalls) > 0 {
		// Tool-call turn: emit tool_use content blocks and stop_reason "tool_use".
		for _, tc := range ollamaResp.Message.ToolCalls {
			resp.Content = append(resp.Content, anthropicResponseBlock{
				Type:  "tool_use",
				ID:    newAnthropicToolUseID(),
				Name:  tc.Function.Name,
				Input: normalizeAnthropicArguments(tc.Function.Arguments),
			})
		}
		resp.StopReason = "tool_use"
		return resp
	}

	resp.Content = append(resp.Content, anthropicResponseBlock{
		Type: "text",
		Text: ollamaResp.Message.Content,
	})
	return resp
}

// newAnthropicToolUseID creates a unique tool_use block ID using crypto/rand.
func newAnthropicToolUseID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("toolu_%016x", time.Now().UnixNano())
	}
	return "toolu_" + hex.EncodeToString(b)
}

// normalizeAnthropicArguments returns args as a valid JSON object, defaulting to
// an empty object when args is empty or invalid.
func normalizeAnthropicArguments(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || !json.Valid(args) {
		return json.RawMessage(`{}`)
	}
	return args
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
		Kind:           anthropicJobKind(tools),
		Priority:       anthropicJobKind(tools).Priority(),
		CreatedAt:      time.Now(),
		Streaming:      true,
		RequestedModel: requestedModel,
		Candidates:     candidates,
		AliasConfig:    aliasConfig,
		Messages:       messages,
		Tools:          tools,
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
	sawToolCalls := false
	blockIndex := 0
	textIndex := 0

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
		toolCalls := resp.Message.ToolCalls

		// Ollama delivers tool calls in a single chunk with empty content. Emit a
		// tool_use content block per call: start (full input) → input_json_delta →
		// stop.
		if len(toolCalls) > 0 {
			sawToolCalls = true
			for _, tc := range toolCalls {
				index := blockIndex
				blockIndex++
				args := normalizeAnthropicArguments(tc.Function.Arguments)

				writeAnthropicSSEEvent(w, "content_block_start", sseContentBlockStart{
					Type:  "content_block_start",
					Index: index,
					ContentBlock: anthropicResponseBlock{
						Type:  "tool_use",
						ID:    newAnthropicToolUseID(),
						Name:  tc.Function.Name,
						Input: args,
					},
				})

				delta, err := json.Marshal(anthropicToolUseDelta{
					Type:        "input_json_delta",
					PartialJSON: string(args),
				})
				if err != nil {
					if logger != nil {
						logger.Warn("streaming: marshal tool_use delta",
							logging.String("error", err.Error()),
						)
					}
					continue
				}
				writeAnthropicSSEEvent(w, "content_block_delta", sseContentBlockDelta{
					Type:  "content_block_delta",
					Index: index,
					Delta: delta,
				})

				writeAnthropicSSEEvent(w, "content_block_stop", sseContentBlockStop{
					Type:  "content_block_stop",
					Index: index,
				})
			}
		}

		// Emit content block events for non-empty content.
		if content != "" {
			if !sentStart {
				// First content chunk: emit content_block_start (empty text) then
				// content_block_delta with the actual text.
				textIndex = blockIndex
				blockIndex++
				sentStart = true
				writeAnthropicSSEEvent(w, "content_block_start", sseContentBlockStart{
					Type:  "content_block_start",
					Index: textIndex,
					ContentBlock: anthropicResponseBlock{
						Type: "text",
						Text: "",
					},
				})
			}

			delta, err := json.Marshal(anthropicTextDelta{
				Type: "text_delta",
				Text: content,
			})
			if err == nil {
				writeAnthropicSSEEvent(w, "content_block_delta", sseContentBlockDelta{
					Type:  "content_block_delta",
					Index: textIndex,
					Delta: delta,
				})
			}
		}

		// Handle done events.
		if resp.Done {
			if sentStart {
				writeAnthropicSSEEvent(w, "content_block_stop", sseContentBlockStop{
					Type:  "content_block_stop",
					Index: textIndex,
				})
			}

			stopReason := mapStopReason(resp.DoneReason)
			if sawToolCalls {
				stopReason = "tool_use"
			}

			writeAnthropicSSEEvent(w, "message_delta", sseMessageDelta{
				Type: "message_delta",
				Delta: anthropicStopDelta{
					StopReason: stopReason,
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

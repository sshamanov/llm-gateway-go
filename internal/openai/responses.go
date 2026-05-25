package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
	"llm-go-proxy/internal/scheduler"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// responsesRequest is the incoming OpenAI Responses API request body (/v1/responses).
type responsesRequest struct {
	Model           string          `json:"model"`
	Input           json.RawMessage `json:"input"`
	Instructions    string          `json:"instructions,omitempty"`
	Stream          *bool           `json:"stream,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
}

// responseInputItem is a single item in the Responses API input array.
type responseInputItem struct {
	Role    string                 `json:"role"`
	Content []responseContentBlock `json:"content"`
}

// responseContentBlock is a content block within a responseInputItem.
type responseContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	FileData string `json:"file_data,omitempty"` // data URL: data:{mime};base64,{data}
	Filename string `json:"filename,omitempty"`
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// responsesResponse is the OpenAI-compatible Responses API response body.
type responsesResponse struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Output  []responsesOutput `json:"output"`
	Usage   responsesUsage    `json:"usage"`
}

// responsesOutput is a single output item (message) in the Responses API response.
type responsesOutput struct {
	Type    string                   `json:"type"`
	ID      string                   `json:"id"`
	Role    string                   `json:"role"`
	Content []responsesOutputContent `json:"content"`
}

// responsesOutputContent is a content block within a responsesOutput.
type responsesOutputContent struct {
	Type        string   `json:"type"`
	Text        string   `json:"text"`
	Annotations []string `json:"annotations"` // always empty
}

// responsesUsage holds token usage statistics.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ---------------------------------------------------------------------------
// File meta helper
// ---------------------------------------------------------------------------

// responsesFileMeta tracks temporary files created from base64 input data
// so they can be cleaned up after the response is sent.
type responsesFileMeta struct {
	paths []string // temp files to clean up
}

// cleanup removes all tracked temporary files. Safe to call on an empty meta.
// Errors from os.Remove are intentionally ignored.
func (m *responsesFileMeta) cleanup() {
	for _, p := range m.paths {
		os.Remove(p)
	}
}

// ---------------------------------------------------------------------------
// Input conversion
// ---------------------------------------------------------------------------

// convertInput handles both string and array input forms.
//
//	string -> [{role: "user", content: "<string>"}]
//	array  -> parse as []responseInputItem, extract text from content blocks
//
// The returned responsesFileMeta (non-nil even on success for the string form)
// must have its cleanup() method called by the caller.
func convertInput(input json.RawMessage, uploadDir string) ([]ollama.ChatMessage, *responsesFileMeta, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("empty input")
	}

	// String form: {"input": "Hello"}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, fmt.Errorf("invalid input string: %w", err)
		}
		return []ollama.ChatMessage{{Role: "user", Content: s}}, &responsesFileMeta{}, nil
	}

	// Array form: {"input": [{...}]}
	var items []responseInputItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, nil, fmt.Errorf("invalid input array: %w", err)
	}

	var messages []ollama.ChatMessage
	meta := &responsesFileMeta{}

	for _, item := range items {
		var sb strings.Builder
		var images []string

		for _, block := range item.Content {
			switch block.Type {
			case "input_text":
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(block.Text)
			case "input_file", "input_image":
				filePath, base64Data, err := spoolBase64File(block.FileData, block.Filename, uploadDir)
				if err != nil {
					meta.cleanup()
					return nil, nil, err
				}
				meta.paths = append(meta.paths, filePath)
				images = append(images, base64Data)
			}
		}

		msg := ollama.ChatMessage{
			Role:    item.Role,
			Content: sb.String(),
		}
		if len(images) > 0 {
			msg.Images = images
		}
		messages = append(messages, msg)
	}

	return messages, meta, nil
}

// convertInstructions wraps instructions as a system message.
// If messages already has a system message as the first message, appends to it.
// If instructions is empty, returns messages unchanged.
func convertInstructions(instructions string, messages []ollama.ChatMessage) []ollama.ChatMessage {
	if instructions == "" {
		return messages
	}

	result := make([]ollama.ChatMessage, 0, len(messages)+1)
	appended := false

	for i, msg := range messages {
		if i == 0 && msg.Role == "system" {
			msg.Content = msg.Content + "\n" + instructions
			appended = true
		}
		result = append(result, msg)
	}

	if !appended {
		// Prepend a new system message.
		systemMsg := ollama.ChatMessage{Role: "system", Content: instructions}
		result = append([]ollama.ChatMessage{systemMsg}, result...)
	}

	return result
}

// ---------------------------------------------------------------------------
// Base64 file handling
// ---------------------------------------------------------------------------

// supportedImageMIMEs maps recognized image MIME types to their file extensions.
var supportedImageMIMEs = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// spoolBase64File parses a data URL, validates the MIME type, decodes the
// base64 payload, and writes the result to a temp file under uploadDir.
//
// For images (image/png, image/jpeg, image/webp, image/gif): also returns the
// full original data URL for use in ChatMessage.Images.
// For non-images: returns an error.
func spoolBase64File(fileData, filename, uploadDir string) (filePath string, base64Data string, err error) {
	if !strings.HasPrefix(fileData, "data:") {
		return "", "", fmt.Errorf("invalid data URL: missing 'data:' prefix")
	}

	rest := strings.TrimPrefix(fileData, "data:")
	semiIdx := strings.Index(rest, ";base64,")
	if semiIdx < 0 {
		return "", "", fmt.Errorf("invalid data URL: missing ';base64,' separator")
	}

	mimeType := rest[:semiIdx]
	base64Payload := rest[semiIdx+len(";base64,"):]

	ext, ok := supportedImageMIMEs[mimeType]
	if !ok {
		return "", "", fmt.Errorf("file type not yet supported: %s", mimeType)
	}

	decoded, err := base64.StdEncoding.DecodeString(base64Payload)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode base64: %w", err)
	}

	// Generate a random filename with the appropriate extension.
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	randomName := hex.EncodeToString(randBytes) + ext
	filePath = filepath.Join(uploadDir, randomName)

	if err := os.WriteFile(filePath, decoded, 0644); err != nil {
		return "", "", fmt.Errorf("failed to write file: %w", err)
	}

	// Return the original data URL so the caller can use it in ChatMessage.Images.
	return filePath, fileData, nil
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// ResponsesHandler returns an HTTP handler for POST /v1/responses.
func ResponsesHandler(logger *logging.Logger, registry *backend.Registry, sched *scheduler.Scheduler, uploadDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Read body (limited to maxRequestBodySize).
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Failed to read request body", "invalid_request_error", "")
			return
		}

		// Step 2: JSON decode into responsesRequest.
		var req responsesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error(), "invalid_request_error", "")
			return
		}

		// Step 3: If Model is empty, reject.
		if req.Model == "" {
			writeJSONError(w, http.StatusBadRequest, "model is required", "invalid_request_error", "")
			return
		}

		// Step 4: Resolve model.
		resolved, err := registry.Resolve(req.Model)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "Unknown model: "+req.Model, "model_not_found", "")
			return
		}

		// Step 5: Convert input (string or array form).
		messages, fileMeta, err := convertInput(req.Input, uploadDir)
		if fileMeta != nil {
			defer fileMeta.cleanup()
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid input: "+err.Error(), "invalid_request_error", "")
			return
		}

		// Step 6: Convert instructions to system message.
		messages = convertInstructions(req.Instructions, messages)

		// Step 7: Build merged options.
		defaults := registry.OllamaDefaults()
		options := &ollama.ChatOptions{
			NumThread:   defaults.Options.NumThread,
			NumCtx:      defaults.Options.NumCtx,
			Temperature: defaults.Options.Temperature,
			TopP:        defaults.Options.TopP,
		}

		if resolved.AliasConfig != nil {
			alias := resolved.AliasConfig
			if alias.Overrides.Options != nil {
				opts := alias.Overrides.Options
				if opts.NumThread != 0 {
					options.NumThread = opts.NumThread
				}
				if opts.NumCtx != 0 {
					options.NumCtx = opts.NumCtx
				}
				if opts.Temperature != 0 {
					options.Temperature = opts.Temperature
				}
				if opts.TopP != 0 {
					options.TopP = opts.TopP
				}
			}
		}

		if registry.Policy().AllowClientOverrideOptions {
			if req.MaxOutputTokens > 0 {
				options.NumPredict = req.MaxOutputTokens
			}
			if req.Temperature != nil {
				options.Temperature = *req.Temperature
			}
			if req.TopP != nil {
				options.TopP = *req.TopP
			}
		}

		// Step 8: If streaming, branch to streaming handler.
		if req.Stream != nil && *req.Stream {
			handleStreamResponses(w, r, logger, sched, req.Model, resolved.Candidates, resolved.AliasConfig, messages, options)
			return
		}

		// Step 9: Generate job ID.
		jobID, err := scheduler.NewJobID()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to generate job ID", "server_error", "")
			return
		}

		// Step 10: Create and submit job to scheduler.
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
			ResultChan:     make(chan scheduler.JobResult, 1),
			JobCtx:         jobCtx,
		}

		if err := sched.Submit(job); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "Queue full: "+err.Error(), "server_error", "queue_full")
			return
		}

		// Step 11: Wait for result or client disconnect.
		var result scheduler.JobResult
		select {
		case result = <-job.ResultChan:
		case <-r.Context().Done():
			return
		}
		if result.Err != nil {
			logger.Error("responses job failed",
				logging.String("model", req.Model),
				logging.String("error", result.Err.Error()),
			)
			writeJSONError(w, http.StatusBadGateway, "Backend error: "+result.Err.Error(), "server_error", "")
			return
		}

		// Step 12: Map response to OpenAI Responses API shape.
		response := mapResponsesResponse(result.Response, req.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to encode responses response",
				logging.String("error", err.Error()),
			)
		}
	})
}

// ---------------------------------------------------------------------------
// Response mapping
// ---------------------------------------------------------------------------

// mapResponsesResponse converts an Ollama ChatResponse to an OpenAI-compatible
// responsesResponse.
func mapResponsesResponse(ollamaResp *ollama.ChatResponse, requestedModel string) responsesResponse {
	respID := generateResponseID()
	return responsesResponse{
		ID:      respID,
		Object:  "response",
		Created: time.Now().Unix(),
		Model:   requestedModel,
		Output: []responsesOutput{
			{
				Type: "message",
				ID:   respID + "_item_0",
				Role: "assistant",
				Content: []responsesOutputContent{
					{
						Type:        "output_text",
						Text:        ollamaResp.Message.Content,
						Annotations: []string{},
					},
				},
			},
		},
		Usage: responsesUsage{
			InputTokens:  ollamaResp.PromptEvalCount,
			OutputTokens: ollamaResp.EvalCount,
			TotalTokens:  ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
		},
	}
}

// ---------------------------------------------------------------------------
// Streaming handler
// ---------------------------------------------------------------------------

// handleStreamResponses processes a streaming Responses API request.
// It creates a streaming job, submits it to the scheduler, reads streaming
// chunks from StreamCh, and writes SSE events to the response writer.
func handleStreamResponses(
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
	// Step 1: Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Step 2: Create a cancellable context derived from the request context.
	streamCtx, cancelStream := context.WithCancel(r.Context())
	defer cancelStream()

	// Step 3: Generate job ID.
	jobID, err := scheduler.NewJobID()
	if err != nil {
		if logger != nil {
			logger.Error("streaming responses: failed to generate job ID",
				logging.String("error", err.Error()),
			)
		}
		return
	}

	// Step 4: Create streaming job.
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
		StreamCh:       streamCh,
		ResultChan:     make(chan scheduler.JobResult, 1),
		JobCtx:         streamCtx,
	}

	// Step 5: Submit to scheduler.
	if err := sched.Submit(job); err != nil {
		errJSON := fmt.Sprintf(`{"error":{"message":"Queue full: %s","type":"server_error","code":"queue_full"}}`, err.Error())
		fmt.Fprintf(w, "data: %s\n\n", errJSON)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	// Step 6: Cancel the stream context when the client disconnects.
	go func() {
		select {
		case <-r.Context().Done():
			cancelStream()
		case <-streamCtx.Done():
			// Already cancelled by normal completion.
		}
	}()

	// Step 7: Generate the response ID (shared across all SSE events for this response).
	respID := generateResponseID()

	// Step 8: Read chunks from the scheduler's stream channel.
	for chunk := range job.StreamCh {
		if chunk == nil {
			continue
		}
		if chunk.Err != nil {
			if logger != nil {
				logger.Warn("streaming responses chunk error",
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
		delta := resp.Message.Content

		// Write delta event for content chunks.
		if delta != "" {
			if err := writeSSEResponseEvent(w, sseResponseEvent{
				Type:        "response.output_text.delta",
				Delta:       delta,
				ItemID:      respID + "_item_0",
				OutputIndex: 0,
				ContentIdx:  0,
			}); err != nil {
				// Client probably disconnected — cancel the backend.
				cancelStream()
				return
			}
		}

		// Write done event for the final chunk.
		if resp.Done {
			fullResp := mapResponsesResponse(resp, requestedModel)
			if err := writeSSEResponseEvent(w, sseResponseEvent{
				Type:     "response.done",
				Response: &fullResp,
			}); err != nil {
				cancelStream()
				return
			}
		}
	}

	// Step 9: Write [DONE] marker.
	writeSSEDone(w)

	// Step 10: Non-blocking read from ResultChan for logging.
	select {
	case result := <-job.ResultChan:
		if result.Err != nil && logger != nil {
			logger.Warn("streaming responses job finished with error",
				logging.String("job_id", jobID),
				logging.String("error", result.Err.Error()),
			)
		}
	default:
		// Result may already have been consumed or not sent.
	}
}

// ---------------------------------------------------------------------------
// ID generation
// ---------------------------------------------------------------------------

// generateResponseID creates a unique Responses API response ID using crypto/rand.
func generateResponseID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen on Linux.
		return fmt.Sprintf("resp_%016x", time.Now().UnixNano())
	}
	return "resp_" + hex.EncodeToString(b)
}

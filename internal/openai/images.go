package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/image"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/scheduler"
)

const maxImgReqBodySize = 1 * 1024 * 1024 // 1MB

// imgErrorResponse is the OpenAI-compatible error envelope for image endpoints.
type imgErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// writeImgError writes an OpenAI-compatible JSON error response.
func writeImgError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(imgErrorResponse{
		Error: struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}{
			Message: message,
			Type:    errType,
		},
	})
}

// imageGenerationRequest is the incoming request body for POST /v1/images/generations.
type imageGenerationRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// imageGenerationResponse is the OpenAI-compatible image generation response body.
type imageGenerationResponse struct {
	Created int64                `json:"created"`
	Data    []imageGenerationData `json:"data"`
}

// imageGenerationData is a single generated image entry.
type imageGenerationData struct {
	URL           string  `json:"url,omitempty"`
	B64JSON       string  `json:"b64_json,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

// ImagesGenerationsHandler returns an HTTP handler for POST /v1/images/generations.
// It acquires host and backend leases directly from the scheduler's lease managers
// (bypassing the scheduler queue) to share host capacity with Ollama backends,
// then calls the image generation API.
func ImagesGenerationsHandler(
	logger *logging.Logger,
	sched *scheduler.Scheduler,
	imageBackends []config.ImageBackendConfig,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Read body limited to maxImgReqBodySize.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxImgReqBodySize))
		if err != nil {
			writeImgError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}

		// Step 2: JSON decode.
		var imgReq imageGenerationRequest
		if err := json.Unmarshal(body, &imgReq); err != nil {
			writeImgError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
			return
		}

		// Step 3: Validate Prompt is non-empty.
		if imgReq.Prompt == "" {
			writeImgError(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
			return
		}

		// Step 4: Apply defaults.
		if imgReq.N == 0 {
			imgReq.N = 1
		}
		if imgReq.Size == "" {
			imgReq.Size = "1024x1024"
		}
		if imgReq.ResponseFormat == "" {
			imgReq.ResponseFormat = "url"
		}

		// Step 5: Find an available image backend.
		var selectedBackend config.ImageBackendConfig
		var found bool
		for _, ib := range imageBackends {
			if !ib.Enabled {
				continue
			}
			if !sched.Scorer.Hosts.AcquireHost(ib.Host) {
				continue
			}
			if !sched.Scorer.Backends.AcquireBackend(ib.ID) {
				sched.Scorer.Hosts.ReleaseHost(ib.Host)
				continue
			}
			selectedBackend = ib
			found = true
			break
		}

		// Step 6: If no backend found, return 503.
		if !found {
			writeImgError(w, http.StatusServiceUnavailable, "api_error", "No image generation backend available")
			return
		}

		// Step 7: Defer release of host and backend leases.
		defer func() {
			sched.Scorer.Hosts.ReleaseHost(selectedBackend.Host)
			sched.Scorer.Backends.ReleaseBackend(selectedBackend.ID)
		}()

		// Step 8: Build GenerationRequest from parsed fields.
		genReq := &image.GenerationRequest{
			Model:          imgReq.Model,
			Prompt:         imgReq.Prompt,
			N:              imgReq.N,
			Size:           imgReq.Size,
			Quality:        imgReq.Quality,
			Style:          imgReq.Style,
			ResponseFormat: imgReq.ResponseFormat,
		}

		// Step 9: Call image.SendGeneration.
		genResp, err := image.SendGeneration(http.DefaultClient, selectedBackend.URL, genReq)
		if err != nil {
			if logger != nil {
				logger.Error("image generation failed",
					logging.String("backend_id", selectedBackend.ID),
					logging.String("error", err.Error()),
				)
			}
			writeImgError(w, http.StatusBadGateway, "api_error", "Backend error: "+err.Error())
			return
		}

		// Step 10: Map response.
		resp := imageGenerationResponse{
			Created: time.Now().Unix(),
			Data:    make([]imageGenerationData, len(genResp.Data)),
		}
		for i, d := range genResp.Data {
			resp.Data[i] = imageGenerationData{
				URL:           d.URL,
				B64JSON:       d.B64JSON,
				RevisedPrompt: d.RevisedPrompt,
			}
		}

		// Step 11: Write 200 JSON response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})
}

package documents

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
)

func writeDocError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ProcessHandler returns an HTTP handler for POST /proxy/documents/process.
//
// The handler accepts a multipart form upload with the following fields:
//   - file  (required) — the document file to process
//   - model (required) — the model to use for inference
//   - mode  (optional) — processing mode; defaults to docCfg.DefaultMode
//
// The file is spooled to disk, prepared into chunks via the prepareQueue, and
// each chunk is submitted as a low-priority KindDocument job through the
// coordinator. The combined inference result is returned as JSON.
func ProcessHandler(
	logger *logging.Logger,
	registry *backend.Registry,
	docCfg config.DocumentsConfig,
	prepareQueue *PrepareQueue,
	coordinator *Coordinator,
	docDir string,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// -----------------------------------------------------------------------
		// 1. Config gate
		// -----------------------------------------------------------------------
		if !docCfg.Enabled {
			writeDocError(w, http.StatusNotFound, "Document processing is not enabled")
			return
		}

		// -----------------------------------------------------------------------
		// 2. Method check
		// -----------------------------------------------------------------------
		if r.Method != http.MethodPost {
			writeDocError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}

		// Generate a short unique request ID (10 hex chars).
		randBytes := make([]byte, 5)
		if _, err := rand.Read(randBytes); err != nil {
			writeDocError(w, http.StatusInternalServerError, "Failed to generate request ID")
			return
		}
		requestID := hex.EncodeToString(randBytes)

		if logger != nil {
			logger.Info("document processing started",
				logging.String("request_id", requestID),
			)
		}

		// -----------------------------------------------------------------------
		// 3. Parse multipart form (max = DefaultMaxBytes + 10 MB overhead)
		// -----------------------------------------------------------------------
		if err := r.ParseMultipartForm(DefaultMaxBytes + 10*1024*1024); err != nil {
			writeDocError(w, http.StatusBadRequest,
				fmt.Sprintf("Failed to parse multipart form: %s", err.Error()),
			)
			return
		}

		// -----------------------------------------------------------------------
		// 4. Extract required fields
		// -----------------------------------------------------------------------
		fileHeaders := r.MultipartForm.File["file"]
		if len(fileHeaders) == 0 {
			writeDocError(w, http.StatusBadRequest, "Missing 'file' field")
			return
		}
		fileHeader := fileHeaders[0]

		model := r.FormValue("model")
		if model == "" {
			writeDocError(w, http.StatusBadRequest, "Missing 'model' field")
			return
		}

		mode := r.FormValue("mode") // optional; defaults later

		// -----------------------------------------------------------------------
		// 5. File type detection
		// -----------------------------------------------------------------------
		ft, err := DetectFileType(fileHeader.Filename)
		if err != nil {
			writeDocError(w, http.StatusBadRequest,
				fmt.Sprintf("Unsupported file type: %s", fileHeader.Filename),
			)
			return
		}

		// Open the multipart file part for spooling.
		file, err := fileHeader.Open()
		if err != nil {
			writeDocError(w, http.StatusBadRequest,
				fmt.Sprintf("Failed to open uploaded file: %s", err.Error()),
			)
			return
		}
		defer file.Close()

		// -----------------------------------------------------------------------
		// 6. Spool file to disk
		// -----------------------------------------------------------------------
		filePath, err := SpoolMultipartFile(file, fileHeader, docDir)
		if err != nil {
			writeDocError(w, 507,
				fmt.Sprintf("Failed to store file: %s", err.Error()),
			)
			return
		}
		defer os.Remove(filePath)

		// -----------------------------------------------------------------------
		// 7. Model resolution
		// -----------------------------------------------------------------------
		resolved, err := registry.Resolve(model)
		if err != nil {
			writeDocError(w, http.StatusNotFound,
				fmt.Sprintf("Unknown model: %s", model),
			)
			return
		}

		// -----------------------------------------------------------------------
		// 8. Resolve processing mode
		// -----------------------------------------------------------------------
		resolvedMode := ResolveMode(mode, Mode(docCfg.DefaultMode))

		// -----------------------------------------------------------------------
		// 9. Vision model availability
		//
		// For v1, we always pass true since the requested model may support vision
		// and the backend detects capability at inference time. Registry-level
		// vision detection can be enhanced later.
		// -----------------------------------------------------------------------
		hasVisionModel := true

		// -----------------------------------------------------------------------
		// 10. Create prepare task
		// -----------------------------------------------------------------------
		task := &PrepareTask{
			ID:             requestID,
			FilePath:       filePath,
			FileType:       ft,
			Mode:           resolvedMode,
			HasVisionModel: hasVisionModel,
			ResultCh:       make(chan *PrepareResult, 1),
		}

		// -----------------------------------------------------------------------
		// 11. Submit to prepare queue
		// -----------------------------------------------------------------------
		if err := prepareQueue.Submit(task); err != nil {
			if logger != nil {
				logger.Error("document preparation queue full",
					logging.String("request_id", requestID),
				)
			}
			writeDocError(w, http.StatusServiceUnavailable, "Preparation queue full")
			return
		}

		// -----------------------------------------------------------------------
		// 12. Wait for preparation result
		// -----------------------------------------------------------------------
		var result *PrepareResult
		select {
		case result = <-task.ResultCh:
		case <-r.Context().Done():
			// Client disconnected while waiting for preparation.
			return
		}

		if result.Err != nil {
			if logger != nil {
				logger.Error("document preparation failed",
					logging.String("request_id", requestID),
					logging.String("error", result.Err.Error()),
				)
			}
			writeDocError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("Document preparation failed: %s", result.Err.Error()),
			)
			return
		}

		// Clean up any page images produced during preparation (e.g. PDF page
		// renders). The parent directory is removed since all page images for
		// this task reside under a single temp directory.
		if len(result.PageImagePaths) > 0 {
			defer os.RemoveAll(filepath.Dir(result.PageImagePaths[0]))
		}

		// -----------------------------------------------------------------------
		// 13. Run chunk inference
		// -----------------------------------------------------------------------
		if len(result.Chunks) == 0 {
			writeDocError(w, http.StatusUnprocessableEntity,
				"Document preparation produced no chunks",
			)
			return
		}

		resp, err := coordinator.Process(
			requestID, model, result.Chunks,
			resolved.Candidates, resolved.AliasConfig,
			nil, // options — use defaults
			r.Context(),
		)
		if err != nil {
			if logger != nil {
				logger.Error("document chunk inference failed",
					logging.String("request_id", requestID),
					logging.String("error", err.Error()),
				)
			}
			writeDocError(w, http.StatusBadGateway,
				fmt.Sprintf("Chunk inference failed: %s", err.Error()),
			)
			return
		}

		// -----------------------------------------------------------------------
		// 14. Set resolved mode in the response
		// -----------------------------------------------------------------------
		resp.Mode = string(result.ResolvedMode)

		// -----------------------------------------------------------------------
		// 15. Return JSON response
		// -----------------------------------------------------------------------
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			if logger != nil {
				logger.Error("failed to encode document response",
					logging.String("request_id", requestID),
					logging.String("error", err.Error()),
				)
			}
		}
	})
}

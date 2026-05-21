package openai

import (
	"encoding/json"
	"net/http"
	"sort"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
)

// modelEntry represents a single model in the OpenAI /v1/models response.
type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// modelsResponse is the top-level response for GET /v1/models.
type modelsResponse struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

// ModelsHandler returns an http.Handler that serves the OpenAI-compatible
// GET /v1/models endpoint.
func ModelsHandler(logger *logging.Logger, registry *backend.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelsConfig := registry.ModelsConfig()
		snapshots := registry.BackendSnapshots()

		// Collect alias names for dedup tracking.
		aliasNames := make(map[string]struct{}, len(modelsConfig.Aliases))
		data := make([]modelEntry, 0, len(modelsConfig.Aliases))

		// Add aliases in config order.
		for _, alias := range modelsConfig.Aliases {
			aliasNames[alias.Name] = struct{}{}
			data = append(data, modelEntry{
				ID:      alias.Name,
				Object:  "model",
				Created: 0,
				OwnedBy: "proxy",
			})
		}

		// Add native Ollama models if enabled.
		if modelsConfig.ExposeNativeOllamaModels {
			// Collect unique native model names with the latest LastContact timestamp.
			nativeCreated := make(map[string]int64)
			for _, snap := range snapshots {
				ts := snap.LastContact.Unix()
				for _, model := range snap.AvailableModels {
					if _, isAlias := aliasNames[model]; isAlias {
						continue
					}
					if existing, ok := nativeCreated[model]; !ok || ts > existing {
						nativeCreated[model] = ts
					}
				}
			}

			// Sort native model names alphabetically.
			nativeModels := make([]string, 0, len(nativeCreated))
			for model := range nativeCreated {
				nativeModels = append(nativeModels, model)
			}
			sort.Strings(nativeModels)

			// Append native model entries.
			for _, model := range nativeModels {
				data = append(data, modelEntry{
					ID:      model,
					Object:  "model",
					Created: nativeCreated[model],
					OwnedBy: "ollama",
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(modelsResponse{
			Object: "list",
			Data:   data,
		})

		logger.Debug("served /v1/models",
			logging.String("method", r.Method),
			logging.Int("model_count", len(data)),
		)
	})
}

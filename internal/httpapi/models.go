package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
)

// debugModelsResponse is the JSON response shape for /debug/models.
type debugModelsResponse struct {
	Aliases      []config.AliasConfig `json:"aliases"`
	NativeModels []string             `json:"native_models"`
	LoadedModels []string             `json:"loaded_models"`
}

// DebugModelsHandler returns a handler that responds with JSON describing
// available models, aliases, and loaded models from the registry.
func DebugModelsHandler(logger *logging.Logger, registry *backend.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := debugModelsResponse{
			Aliases:      make([]config.AliasConfig, 0),
			NativeModels: make([]string, 0),
			LoadedModels: make([]string, 0),
		}

		if registry != nil {
			mc := registry.ModelsConfig()
			if len(mc.Aliases) > 0 {
				resp.Aliases = mc.Aliases
			}

			snapshots := registry.BackendSnapshots()

			if mc.ExposeNativeOllamaModels {
				nativeSet := make(map[string]struct{})
				for _, snap := range snapshots {
					for _, m := range snap.AvailableModels {
						nativeSet[m] = struct{}{}
					}
				}
				resp.NativeModels = stringSetSorted(nativeSet)
			}

			loadedSet := make(map[string]struct{})
			for _, snap := range snapshots {
				for _, m := range snap.LoadedModels {
					loadedSet[m] = struct{}{}
				}
			}
			resp.LoadedModels = stringSetSorted(loadedSet)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
		logger.Debug("debug models")
	})
}

// stringSetSorted returns sorted keys from a set map.
func stringSetSorted(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

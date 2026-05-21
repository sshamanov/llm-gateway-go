package backend

import (
	"fmt"

	"llm-go-proxy/internal/config"
)

// ResolutionKind indicates how a model ID was resolved.
type ResolutionKind int

const (
	// ResolutionAlias means the model ID matched a configured alias.
	ResolutionAlias ResolutionKind = iota
	// ResolutionNative means the model ID matched a native Ollama model name
	// that exists on at least one backend.
	ResolutionNative
)

// ResolvedModel holds the result of model ID resolution.
type ResolvedModel struct {
	Kind        ResolutionKind
	RequestedID string
	Candidates  []string
	AliasConfig *config.AliasConfig // Only set when Kind == ResolutionAlias
}

// Resolve resolves a model ID to candidate model names.
// Resolution order (ARCH §7.3):
//  1. Exact alias match -> candidates = [primary, backups...]
//  2. Native Ollama model name (if expose_native_ollama_models=true and model
//     exists on at least one backend)
//  3. Error: "unknown model: <modelID>"
func (r *Registry) Resolve(modelID string) (*ResolvedModel, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}

	mc := r.ModelsConfig()

	// Step 1: Check for exact alias match.
	for i := range mc.Aliases {
		if mc.Aliases[i].Name == modelID {
			candidates := make([]string, 0, 1+len(mc.Aliases[i].BackupModels))
			candidates = append(candidates, mc.Aliases[i].PrimaryModel)
			candidates = append(candidates, mc.Aliases[i].BackupModels...)
			return &ResolvedModel{
				Kind:        ResolutionAlias,
				RequestedID: modelID,
				Candidates:  candidates,
				AliasConfig: &mc.Aliases[i],
			}, nil
		}
	}

	// Step 2: Check native Ollama model (if exposed).
	if mc.ExposeNativeOllamaModels {
		for _, snap := range r.BackendSnapshots() {
			for _, m := range snap.AvailableModels {
				if m == modelID {
					return &ResolvedModel{
						Kind:        ResolutionNative,
						RequestedID: modelID,
						Candidates:  []string{modelID},
					}, nil
				}
			}
		}
	}

	// Step 3: Unknown model.
	return nil, fmt.Errorf("unknown model: %s", modelID)
}

// FindFirstBackend returns the first backend that is enabled, healthy, and has
// the given model in its AvailableModels list.
func FindFirstBackend(snapshots []BackendSnapshot, modelName string) (*BackendSnapshot, error) {
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no backends available")
	}
	for i := range snapshots {
		if !snapshots[i].Enabled {
			continue
		}
		if !snapshots[i].Healthy {
			continue
		}
		for _, m := range snapshots[i].AvailableModels {
			if m == modelName {
				return &snapshots[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no backend found for model: %s", modelName)
}

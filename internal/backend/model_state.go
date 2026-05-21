package backend

import (
	"slices"

	"llm-go-proxy/internal/ollama"
)

// ModelNamesFromTags extracts model names from a TagsResponse.
// Returns deduplicated, sorted names. Returns nil if tags is nil.
func ModelNamesFromTags(tags *ollama.TagsResponse) []string {
	if tags == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(tags.Models))
	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		if _, ok := seen[m.Name]; !ok {
			seen[m.Name] = struct{}{}
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

// ModelNamesFromPS extracts model names from a PSResponse.
// Uses PSModel.Name (not .Model) as the human-readable name.
// Returns deduplicated, sorted names. Returns nil if ps is nil.
func ModelNamesFromPS(ps *ollama.PSResponse) []string {
	if ps == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(ps.Models))
	names := make([]string, 0, len(ps.Models))
	for _, m := range ps.Models {
		if _, ok := seen[m.Name]; !ok {
			seen[m.Name] = struct{}{}
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

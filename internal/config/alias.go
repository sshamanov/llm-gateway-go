package config

import "fmt"

const maxExtendsDepth = 10

// resolveAliasInheritance resolves extends chains in-place.
// Aliases that extend others inherit their parent's PrimaryModel, BackupModels,
// and Overrides (deep-merged, child wins).
// Circular references and missing parents are errors.
func resolveAliasInheritance(aliases []AliasConfig) ([]AliasConfig, error) {
	if len(aliases) == 0 {
		return aliases, nil
	}

	byName := make(map[string]int, len(aliases))
	for i := range aliases {
		if aliases[i].Name == "" {
			return nil, fmt.Errorf("alias at index %d has empty name", i)
		}
		if _, exists := byName[aliases[i].Name]; exists {
			return nil, fmt.Errorf("duplicate alias name: %q", aliases[i].Name)
		}
		byName[aliases[i].Name] = i
	}

	// Memoize resolved aliases to avoid repeated chain walks.
	resolved := make(map[string]*AliasConfig, len(aliases))

	for i := range aliases {
		merged, err := resolveOne(aliases[i].Name, aliases, byName, resolved, 0)
		if err != nil {
			return nil, err
		}
		aliases[i] = *merged
	}

	return aliases, nil
}

func resolveOne(
	name string,
	aliases []AliasConfig,
	byName map[string]int,
	resolved map[string]*AliasConfig,
	depth int,
) (*AliasConfig, error) {
	if depth > maxExtendsDepth {
		return nil, fmt.Errorf("alias %q: extends chain too deep (>%d)", name, maxExtendsDepth)
	}

	if r, ok := resolved[name]; ok {
		return r, nil
	}

	idx, ok := byName[name]
	if !ok {
		return nil, fmt.Errorf("alias %q: not found in aliases list", name)
	}

	a := aliases[idx]

	if a.Extends == "" {
		// Leaf — no inheritance.
		if a.PrimaryModel == "" {
			return nil, fmt.Errorf("alias %q: primary_model is required (no extends)", name)
		}
		resolved[name] = &a
		return &a, nil
	}

	// Recursively resolve parent.
	parent, err := resolveOne(a.Extends, aliases, byName, resolved, depth+1)
	if err != nil {
		return nil, err
	}

	// Merge: start from parent, overlay child.
	merged := *parent
	merged.Name = a.Name
	merged.Extends = "" // flattened

	if a.PrimaryModel != "" {
		merged.PrimaryModel = a.PrimaryModel
	}
	if len(a.BackupModels) > 0 {
		merged.BackupModels = a.BackupModels
	}

	// Deep-merge Overrides.
	merged.Overrides = mergeOverrides(parent.Overrides, a.Overrides)

	resolved[name] = &merged
	return &merged, nil
}

func mergeOverrides(base, child AliasOverrides) AliasOverrides {
	if child.KeepAlive != "" {
		base.KeepAlive = child.KeepAlive
	}
	if child.Think != nil {
		base.Think = child.Think
	}
	if child.Options != nil {
		if base.Options == nil {
			base.Options = &OllamaOptions{}
		}
		co := child.Options
		bo := base.Options
		if co.NumThread != 0 {
			bo.NumThread = co.NumThread
		}
		if co.NumCtx != 0 {
			bo.NumCtx = co.NumCtx
		}
		if co.Temperature != 0 {
			bo.Temperature = co.Temperature
		}
		if co.TopP != 0 {
			bo.TopP = co.TopP
		}
		if co.TopK != 0 {
			bo.TopK = co.TopK
		}
		if co.RepeatPenalty != 0 {
			bo.RepeatPenalty = co.RepeatPenalty
		}
		if co.NumPredict != 0 {
			bo.NumPredict = co.NumPredict
		}
	}
	return base
}

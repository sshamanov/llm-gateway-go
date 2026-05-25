package config

import (
	"testing"
)

func TestResolveAliasInheritance_Basic(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "base",
			PrimaryModel: "qwen:30b",
			BackupModels: []string{"qwen:14b"},
			Overrides: AliasOverrides{
				Think: ptrBool(false),
				Options: &OllamaOptions{
					NumCtx:      8192,
					Temperature: 0.2,
				},
			},
		},
		{
			Name:    "coding",
			Extends: "base",
			Overrides: AliasOverrides{
				Options: &OllamaOptions{
					Temperature: 0.1,
				},
			},
		},
	}

	resolved, err := resolveAliasInheritance(aliases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// base should be unchanged.
	if resolved[0].PrimaryModel != "qwen:30b" {
		t.Errorf("base primary: got %q", resolved[0].PrimaryModel)
	}
	if resolved[0].Overrides.Options.NumCtx != 8192 {
		t.Errorf("base num_ctx: got %d", resolved[0].Overrides.Options.NumCtx)
	}

	// coding should inherit PrimaryModel and BackupModels from base.
	coding := resolved[1]
	if coding.PrimaryModel != "qwen:30b" {
		t.Errorf("coding primary: got %q", coding.PrimaryModel)
	}
	if len(coding.BackupModels) != 1 || coding.BackupModels[0] != "qwen:14b" {
		t.Errorf("coding backups: %v", coding.BackupModels)
	}
	// coding overrides temperature but inherits NumCtx.
	if coding.Overrides.Options.Temperature != 0.1 {
		t.Errorf("coding temperature: got %f", coding.Overrides.Options.Temperature)
	}
	if coding.Overrides.Options.NumCtx != 8192 {
		t.Errorf("coding num_ctx: got %d, want 8192 (inherited)", coding.Overrides.Options.NumCtx)
	}
	// coding inherits think.
	if coding.Overrides.Think == nil || *coding.Overrides.Think != false {
		t.Error("coding think should be false (inherited)")
	}
	// extends field should be cleared after resolution.
	if coding.Extends != "" {
		t.Errorf("coding extends should be empty after resolution: %q", coding.Extends)
	}
}

func TestResolveAliasInheritance_ChildOverridesModels(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "base",
			PrimaryModel: "qwen:30b",
			BackupModels: []string{"qwen:14b"},
		},
		{
			Name:         "custom",
			Extends:      "base",
			PrimaryModel: "qwen:72b",
			BackupModels: []string{"qwen:30b"},
		},
	}

	resolved, err := resolveAliasInheritance(aliases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	custom := resolved[1]
	if custom.PrimaryModel != "qwen:72b" {
		t.Errorf("custom primary: got %q", custom.PrimaryModel)
	}
	if len(custom.BackupModels) != 1 || custom.BackupModels[0] != "qwen:30b" {
		t.Errorf("custom backups: %v", custom.BackupModels)
	}
}

func TestResolveAliasInheritance_ChainOfThree(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "base",
			PrimaryModel: "m1",
			Overrides: AliasOverrides{
				Options: &OllamaOptions{Temperature: 0.5},
			},
		},
		{
			Name:    "mid",
			Extends: "base",
			Overrides: AliasOverrides{
				Options: &OllamaOptions{TopP: 0.9},
			},
		},
		{
			Name:    "leaf",
			Extends: "mid",
			Overrides: AliasOverrides{
				Options: &OllamaOptions{Temperature: 0.1},
			},
		},
	}

	resolved, err := resolveAliasInheritance(aliases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	leaf := resolved[2]
	if leaf.PrimaryModel != "m1" {
		t.Errorf("leaf primary: got %q", leaf.PrimaryModel)
	}
	if leaf.Overrides.Options.Temperature != 0.1 {
		t.Errorf("leaf temperature: got %f", leaf.Overrides.Options.Temperature)
	}
	if leaf.Overrides.Options.TopP != 0.9 {
		t.Errorf("leaf top_p: got %f, want 0.9 (inherited from mid)", leaf.Overrides.Options.TopP)
	}
}

func TestResolveAliasInheritance_Circular(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "a",
			Extends:      "b",
			PrimaryModel: "m1",
		},
		{
			Name:         "b",
			Extends:      "a",
			PrimaryModel: "m1",
		},
	}

	_, err := resolveAliasInheritance(aliases)
	if err == nil {
		t.Fatal("expected error for circular extends")
	}
}

func TestResolveAliasInheritance_MissingParent(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "orphan",
			Extends:      "nonexistent",
			PrimaryModel: "m1",
		},
	}

	_, err := resolveAliasInheritance(aliases)
	if err == nil {
		t.Fatal("expected error for missing parent")
	}
}

func TestResolveAliasInheritance_NoExtends(t *testing.T) {
	aliases := []AliasConfig{
		{
			Name:         "standalone",
			PrimaryModel: "m1",
			Overrides: AliasOverrides{
				Options: &OllamaOptions{Temperature: 0.3},
			},
		},
	}

	resolved, err := resolveAliasInheritance(aliases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved[0].PrimaryModel != "m1" {
		t.Errorf("primary: got %q", resolved[0].PrimaryModel)
	}
}

func TestResolveAliasInheritance_EmptyAliases(t *testing.T) {
	resolved, err := resolveAliasInheritance(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 0 {
		t.Errorf("expected empty, got %d", len(resolved))
	}
}

func TestResolveAliasInheritance_DuplicateName(t *testing.T) {
	aliases := []AliasConfig{
		{Name: "dup", PrimaryModel: "m1"},
		{Name: "dup", PrimaryModel: "m2"},
	}

	_, err := resolveAliasInheritance(aliases)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

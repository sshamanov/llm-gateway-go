package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPaths(t *testing.T) {
	tests := []struct {
		name string
		root string
		want Paths
	}{
		{
			name: "standard root",
			root: "/data",
			want: Paths{
				Root:       "/data",
				ConfigFile: "/data/config/config.json",
				Tmp:        "/data/tmp",
				Cache:      "/data/cache",
				Uploads:    "/data/uploads",
				Documents:  "/data/documents",
				Images:     "/data/images",
				Audio:      "/data/audio",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPaths(tt.root)
			if got != tt.want {
				t.Errorf("NewPaths(%q) = %+v, want %+v", tt.root, got, tt.want)
			}
		})
	}
}

func TestNewPaths_TrailingSlash(t *testing.T) {
	tests := []struct {
		name string
		root string
		want Paths
	}{
		{
			name: "trailing slash normalized",
			root: "/data/",
			want: Paths{
				Root:       "/data/",
				ConfigFile: "/data/config/config.json",
				Tmp:        "/data/tmp",
				Cache:      "/data/cache",
				Uploads:    "/data/uploads",
				Documents:  "/data/documents",
				Images:     "/data/images",
				Audio:      "/data/audio",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPaths(tt.root)

			// filepath.Join normalizes trailing slashes, so the derived
			// paths should match those produced with "/data".
			if got.ConfigFile != tt.want.ConfigFile {
				t.Errorf("ConfigFile = %q, want %q", got.ConfigFile, tt.want.ConfigFile)
			}
			if got.Tmp != tt.want.Tmp {
				t.Errorf("Tmp = %q, want %q", got.Tmp, tt.want.Tmp)
			}
			if got.Cache != tt.want.Cache {
				t.Errorf("Cache = %q, want %q", got.Cache, tt.want.Cache)
			}
			if got.Uploads != tt.want.Uploads {
				t.Errorf("Uploads = %q, want %q", got.Uploads, tt.want.Uploads)
			}
			if got.Documents != tt.want.Documents {
				t.Errorf("Documents = %q, want %q", got.Documents, tt.want.Documents)
			}
			if got.Images != tt.want.Images {
				t.Errorf("Images = %q, want %q", got.Images, tt.want.Images)
			}
			if got.Audio != tt.want.Audio {
				t.Errorf("Audio = %q, want %q", got.Audio, tt.want.Audio)
			}
		})
	}
}

func TestNewPaths_EmptyRoot(t *testing.T) {
	tests := []struct {
		name string
		root string
	}{
		{
			name: "empty root yields relative paths",
			root: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewPaths(tt.root)

			// With an empty root, filepath.Join produces relative paths.
			if got.ConfigFile != "config/config.json" {
				t.Errorf("ConfigFile = %q, want %q", got.ConfigFile, "config/config.json")
			}
			if got.Tmp != "tmp" {
				t.Errorf("Tmp = %q, want %q", got.Tmp, "tmp")
			}
			if got.Cache != "cache" {
				t.Errorf("Cache = %q, want %q", got.Cache, "cache")
			}
			if got.Uploads != "uploads" {
				t.Errorf("Uploads = %q, want %q", got.Uploads, "uploads")
			}
			if got.Documents != "documents" {
				t.Errorf("Documents = %q, want %q", got.Documents, "documents")
			}
			if got.Images != "images" {
				t.Errorf("Images = %q, want %q", got.Images, "images")
			}
			if got.Audio != "audio" {
				t.Errorf("Audio = %q, want %q", got.Audio, "audio")
			}
		})
	}
}

func TestEnsureDirs_CreatesDirectories(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)

	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs() unexpected error: %v", err)
	}

	// Verify each subdirectory was created.
	subdirs := []struct {
		name string
		path string
	}{
		{"Config", filepath.Dir(p.ConfigFile)},
		{"Tmp", p.Tmp},
		{"Cache", p.Cache},
		{"Uploads", p.Uploads},
		{"Documents", p.Documents},
		{"Images", p.Images},
		{"Audio", p.Audio},
	}

	for _, sd := range subdirs {
		info, err := os.Stat(sd.path)
		if err != nil {
			t.Errorf("expected %s (%s) to exist, got error: %v", sd.name, sd.path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s (%s) to be a directory", sd.name, sd.path)
		}
	}
}

func TestEnsureDirs_Idempotent(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)

	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("first EnsureDirs() unexpected error: %v", err)
	}

	// Second call should also succeed.
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("second EnsureDirs() unexpected error: %v", err)
	}
}

func TestEnsureDirs_NonExistentParent(t *testing.T) {
	// Use a path inside a temporary directory that does not exist.
	base := t.TempDir()
	nonExistentRoot := filepath.Join(base, "nonexistent", "subdir")
	p := NewPaths(nonExistentRoot)

	err := p.EnsureDirs()
	if err == nil {
		t.Fatal("expected an error for non-existent root directory, got nil")
	}

	// The error should reference the missing root directory.
	want := "storage root directory does not exist"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestConfigFilePath(t *testing.T) {
	tests := []struct {
		name string
		root string
		want string
	}{
		{
			name: "standard root",
			root: "/data",
			want: "/data/config/config.json",
		},
		{
			name: "empty root",
			root: "",
			want: "config/config.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPaths(tt.root)
			got := p.ConfigFilePath()
			if got != tt.want {
				t.Errorf("ConfigFilePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

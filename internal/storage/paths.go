package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds all derived storage paths from the root storage directory.
type Paths struct {
	Root       string
	ConfigFile string
	Tmp        string
	Cache      string
	Uploads    string
	Documents  string
	Images     string
	Audio      string
}

// NewPaths creates a Paths value with all derived paths computed from root
// using filepath.Join (which handles normalization, including trailing slashes).
func NewPaths(root string) Paths {
	return Paths{
		Root:       root,
		ConfigFile: filepath.Join(root, "config", "config.json"),
		Tmp:        filepath.Join(root, "tmp"),
		Cache:      filepath.Join(root, "cache"),
		Uploads:    filepath.Join(root, "uploads"),
		Documents:  filepath.Join(root, "documents"),
		Images:     filepath.Join(root, "images"),
		Audio:      filepath.Join(root, "audio"),
	}
}

// EnsureDirs creates all subdirectories (not Root itself and not the parent of
// ConfigFile) with mode 0755. Returns an error if Root does not exist or if
// any directory creation fails.
func (p Paths) EnsureDirs() error {
	// Defensive check: the root directory must already exist (e.g. created by
	// Docker volume mount).
	info, err := os.Stat(p.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("storage root directory does not exist: %s", p.Root)
		}
		return fmt.Errorf("checking storage root %s: %w", p.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage root is not a directory: %s", p.Root)
	}

	dirs := []string{
		p.Tmp,
		p.Cache,
		p.Uploads,
		p.Documents,
		p.Images,
		p.Audio,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	return nil
}

// ConfigFilePath returns the path to the configuration file.
func (p Paths) ConfigFilePath() string {
	return p.ConfigFile
}

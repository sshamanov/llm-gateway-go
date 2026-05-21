package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// errEmptyConfig is returned when the config file is empty.
var errEmptyConfig = errors.New("config file is empty")

// LoadConfig reads a JSON config file at path and returns a Config.
//
// It starts from DefaultConfig() and then overlays values from the JSON file.
// If the file does not exist (os.IsNotExist), the defaults are returned with no error.
// On parse failure a zero Config and the parse error are returned so callers
// can distinguish "no config" from "broken config".
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	if len(data) == 0 {
		return Config{}, errEmptyConfig
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}

	return cfg, nil
}

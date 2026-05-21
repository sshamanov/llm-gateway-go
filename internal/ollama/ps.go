package ollama

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FetchPS fetches the list of currently loaded models from an Ollama backend.
// It calls GET {baseURL}/api/ps and returns the parsed response.
func FetchPS(client *http.Client, baseURL string) (*PSResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/ps"

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch ps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch ps: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch ps: read body: %w", err)
	}

	var ps PSResponse
	if err := json.Unmarshal(body, &ps); err != nil {
		return nil, fmt.Errorf("fetch ps: decode body: %w", err)
	}

	return &ps, nil
}

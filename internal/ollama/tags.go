package ollama

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FetchTags fetches the list of available models from an Ollama backend.
// It calls GET {baseURL}/api/tags and returns the parsed response.
func FetchTags(client *http.Client, baseURL string) (*TagsResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/tags"

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch tags: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: read body: %w", err)
	}

	var tags TagsResponse
	if err := json.Unmarshal(body, &tags); err != nil {
		return nil, fmt.Errorf("fetch tags: decode body: %w", err)
	}

	return &tags, nil
}

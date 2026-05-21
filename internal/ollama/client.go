package ollama

import (
	"net/http"
	"time"
)

// NewHTTPClient returns an *http.Client with a 30-second timeout.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
	}
}

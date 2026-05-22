package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"llm-go-proxy/internal/image"
)

// FakeImageBackend is a minimal fake image generation endpoint.
type FakeImageBackend struct {
	Server *httptest.Server
	URL    string
}

// NewFakeImageBackend creates and starts a fake image generation backend.
func NewFakeImageBackend() *FakeImageBackend {
	server := httptest.NewServer(fakeImageHandler())
	return &FakeImageBackend{
		Server: server,
		URL:    server.URL,
	}
}

func fakeImageHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /images/generations", func(w http.ResponseWriter, r *http.Request) {
		resp := image.GenerationResponse{
			Created: time.Now().Unix(),
			Data: []image.GenerationData{
				{B64JSON: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return mux
}

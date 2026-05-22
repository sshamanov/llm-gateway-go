package mock

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"time"

	"llm-go-proxy/internal/ollama"
)

// FakeOllamaConfig configures a FakeOllama instance.
type FakeOllamaConfig struct {
	ID                  string
	Models              []string
	LoadedModels        []string
	ChatLatency         time.Duration
	StreamChunkLatency  time.Duration
	FailureRate         float64
	FirstChunkGarbage   bool
	MidStreamFailAfter  int
	ChatResponseContent string
}

// FakeOllama wraps httptest.Server as a fully independent fake Ollama node.
type FakeOllama struct {
	Server *httptest.Server
	URL    string
	ID     string
}

// NewFakeOllama creates and starts a fake Ollama backend.
func NewFakeOllama(cfg FakeOllamaConfig) *FakeOllama {
	if cfg.ID == "" {
		cfg.ID = "default"
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"llama3"}
	}
	if len(cfg.LoadedModels) == 0 {
		cfg.LoadedModels = cfg.Models
	}
	if cfg.ChatResponseContent == "" {
		cfg.ChatResponseContent = "Hello from FakeOllama"
	}
	if cfg.StreamChunkLatency == 0 {
		cfg.StreamChunkLatency = 5 * time.Millisecond
	}

	handler := fakeOllamaHandler(cfg)
	server := httptest.NewServer(handler)

	return &FakeOllama{
		Server: server,
		URL:    server.URL,
		ID:     cfg.ID,
	}
}

func fakeOllamaHandler(cfg FakeOllamaConfig) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		models := make([]ollama.TagsModel, len(cfg.Models))
		for i, name := range cfg.Models {
			models[i] = ollama.TagsModel{Name: name}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollama.TagsResponse{Models: models})
	})

	mux.HandleFunc("GET /api/ps", func(w http.ResponseWriter, r *http.Request) {
		models := make([]ollama.PSModel, len(cfg.LoadedModels))
		for i, name := range cfg.LoadedModels {
			models[i] = ollama.PSModel{Name: name, Model: name}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: models})
	})

	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		if cfg.FailureRate > 0 && rand.Float64() < cfg.FailureRate {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var chatReq ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if chatReq.Stream {
			handleStreamingChat(w, cfg, chatReq.Model)
		} else {
			handleNonStreamingChat(w, cfg, chatReq.Model)
		}
	})

	return mux
}

func handleNonStreamingChat(w http.ResponseWriter, cfg FakeOllamaConfig, model string) {
	if cfg.ChatLatency > 0 {
		time.Sleep(cfg.ChatLatency)
	}

	resp := ollama.ChatResponse{
		Model:     model,
		CreatedAt: time.Now(),
		Message: ollama.ChatMessage{
			Role:    "assistant",
			Content: cfg.ChatResponseContent,
		},
		Done:             true,
		TotalDuration:    int64(100 * time.Millisecond),
		LoadDuration:     0,
		PromptEvalCount:  10,
		EvalCount:        5,
		DoneReason:       "stop",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleStreamingChat(w http.ResponseWriter, cfg FakeOllamaConfig, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	// First chunk: garbage injection.
	if cfg.FirstChunkGarbage {
		fmt.Fprintf(w, "{garbage\n")
		flusher.Flush()
		return
	}

	// Send content chunks.
	words := []string{"Hello", " from", " Fake", "Ollama"}
	chunksSent := 0

	for i, word := range words {
		if cfg.MidStreamFailAfter > 0 && chunksSent >= cfg.MidStreamFailAfter {
			fmt.Fprintf(w, "{corrupt\n")
			flusher.Flush()
			return
		}

		done := i == len(words)-1
		chunk := ollama.ChatResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Message: ollama.ChatMessage{
				Role:    "assistant",
				Content: word,
			},
			Done: done,
		}
		if done {
			chunk.TotalDuration = int64(200 * time.Millisecond)
			chunk.EvalCount = 5
			chunk.DoneReason = "stop"
		}

		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "%s\n", b)
		flusher.Flush()
		chunksSent++

		if cfg.StreamChunkLatency > 0 && !done {
			time.Sleep(cfg.StreamChunkLatency)
		}
	}
}

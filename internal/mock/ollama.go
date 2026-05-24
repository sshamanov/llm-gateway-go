package mock

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
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
	FailureRate         float64        // Random failure probability (0..1) per request
	FailCount           int            // Deterministic: fail first N requests, then succeed
	FirstChunkGarbage   bool
	MidStreamFailAfter  int
	ChatResponseContent string
	LoadDuration        int64          // Fake model load time in nanoseconds
	TotalDuration       int64          // Fake total inference time in nanoseconds
	EvalCount           int            // Fake eval token count for TPS calculation
}

// requestCount tracks per-backend request counts for deterministic failure.
type requestCount struct {
	mu    sync.Mutex
	count int
}

func (rc *requestCount) inc() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.count++
	return rc.count
}

// FakeOllama wraps httptest.Server as a fully independent fake Ollama node.
type FakeOllama struct {
	Server    *httptest.Server
	URL       string
	ID        string
	reqCount  *requestCount
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

	rc := &requestCount{}
	handler := fakeOllamaHandler(cfg, rc)
	server := httptest.NewServer(handler)

	return &FakeOllama{
		Server:   server,
		URL:      server.URL,
		ID:       cfg.ID,
		reqCount: rc,
	}
}

func fakeOllamaHandler(cfg FakeOllamaConfig, rc *requestCount) http.Handler {
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

		if cfg.FailCount > 0 && rc.inc() <= cfg.FailCount {
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

	totalDur := cfg.TotalDuration
	if totalDur == 0 {
		totalDur = int64(100 * time.Millisecond)
	}
	evalCount := cfg.EvalCount
	if evalCount == 0 {
		evalCount = 5
	}

	resp := ollama.ChatResponse{
		Model:     model,
		CreatedAt: time.Now(),
		Message: ollama.ChatMessage{
			Role:    "assistant",
			Content: cfg.ChatResponseContent,
		},
		Done:             true,
		TotalDuration:    totalDur,
		LoadDuration:     cfg.LoadDuration,
		PromptEvalCount:  10,
		EvalCount:        evalCount,
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

	totalDur := cfg.TotalDuration
	if totalDur == 0 {
		totalDur = int64(200 * time.Millisecond)
	}
	evalCount := cfg.EvalCount
	if evalCount == 0 {
		evalCount = 5
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
			chunk.TotalDuration = totalDur
			chunk.LoadDuration = cfg.LoadDuration
			chunk.EvalCount = evalCount
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

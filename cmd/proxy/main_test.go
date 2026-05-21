package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/storage"
)

func TestServerHealthAndReadinessEndpoints(t *testing.T) {
	// Create a temp directory as the storage root.
	tmpDir := t.TempDir()

	// Create the config directory and write a minimal config.json.
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	// A minimal config — empty JSON overlays on top of DefaultConfig().
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Set environment variables so that run() would pick them up.
	t.Setenv("PROXY_STORAGE_DIR", tmpDir)
	t.Setenv("PROXY_LISTEN", ":0")

	// Set up paths and ensure subdirectories exist.
	paths := storage.NewPaths(tmpDir)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Verify config loads cleanly from the file we wrote.
	if _, err := config.LoadConfig(paths.ConfigFilePath()); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	logger := logging.NewLogger(logging.LevelDebug, "proxy-test")
	router := httpapi.NewRouter(logger, nil, nil)

	srv := &http.Server{
		Handler:        router,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   5 * time.Second,
		IdleTimeout:    5 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Addr = listener.Addr().String()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	baseURL := "http://" + srv.Addr

	// Give the server a moment to start.
	time.Sleep(50 * time.Millisecond)

	// Test /healthz returns 200 and {"status":"ok"}.
	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decoding response body: %v", err)
		}
		if body["status"] != "ok" {
			t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
		}
	})

	// Test /readyz returns 200 and {"status":"ready"}.
	t.Run("readyz", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decoding response body: %v", err)
		}
		if body["status"] != "ready" {
			t.Errorf(`body["status"] = %q, want "ready"`, body["status"])
		}
	})

	// Trigger graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify the server stopped without errors.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
}

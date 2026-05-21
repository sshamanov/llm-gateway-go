package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "proxy: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddr := os.Getenv("PROXY_LISTEN")
	if listenAddr == "" {
		listenAddr = ":4000"
	}

	storageDir := os.Getenv("PROXY_STORAGE_DIR")
	if storageDir == "" {
		storageDir = "/data"
	}

	paths := storage.NewPaths(storageDir)
	if err := paths.EnsureDirs(); err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	cfg, err := config.LoadConfig(paths.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logLevel := logging.LevelInfo
	if cfg.Server.EnableDebugLogging {
		logLevel = logging.LevelDebug
	}
	logger := logging.NewLogger(logLevel, "proxy")

	router := httpapi.NewRouter(logger)

	srv := &http.Server{
		Addr:           listenAddr,
		Handler:        router,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   time.Duration(cfg.Server.RequestTimeoutSeconds) * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	logger.Info("starting proxy",
		logging.String("listen_addr", listenAddr),
		logging.String("storage_dir", storageDir),
		logging.String("config_path", paths.ConfigFilePath()),
	)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", logging.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutting down", logging.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}

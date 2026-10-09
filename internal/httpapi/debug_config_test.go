package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-go-proxy/internal/config"
)

func TestDebugConfigRedactsAPIKeys(t *testing.T) {
	cfg := &config.Config{
		ImageBackends: []config.ImageBackendConfig{{ID: "img", APIKey: "sk-image-secret"}},
		AudioBackends: []config.AudioBackendConfig{{ID: "audio", APIKey: "sk-audio-secret"}},
	}
	rec := httptest.NewRecorder()
	DebugConfigHandler(nil, cfg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/config", nil))

	body := rec.Body.String()
	if strings.Contains(body, "sk-image-secret") || strings.Contains(body, "sk-audio-secret") {
		t.Fatalf("api key leaked in /debug/config: %s", body)
	}
	if strings.Count(body, redacted) != 2 {
		t.Fatalf("expected 2 redacted keys, got: %s", body)
	}
	if cfg.ImageBackends[0].APIKey != "sk-image-secret" || cfg.AudioBackends[0].APIKey != "sk-audio-secret" {
		t.Fatal("redaction mutated the live config")
	}
}

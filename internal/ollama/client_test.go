package ollama

import (
	"testing"
	"time"
)

func TestNewHTTPClient_Timeout(t *testing.T) {
	client := NewHTTPClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", client.Timeout)
	}
}

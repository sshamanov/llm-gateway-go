package image

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestSendGeneration_Success(t *testing.T) {
    revised := "A revised prompt describing a cat"
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/images/generations" {
            t.Errorf("unexpected path: %s", r.URL.Path)
        }
        if r.Method != http.MethodPost {
            t.Errorf("unexpected method: %s", r.Method)
        }

        var req GenerationRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            t.Errorf("failed to decode request body: %v", err)
        }
        if req.Prompt != "a cat" {
            t.Errorf("expected Prompt 'a cat', got %q", req.Prompt)
        }
        if req.N != 1 {
            t.Errorf("expected N 1, got %d", req.N)
        }
        if req.Size != "1024x1024" {
            t.Errorf("expected Size '1024x1024', got %q", req.Size)
        }

        resp := GenerationResponse{
            Created: 1719000000,
            Data: []GenerationData{
                {
                    URL:           "https://example.com/image.png",
                    RevisedPrompt: &revised,
                },
            },
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        if err := json.NewEncoder(w).Encode(resp); err != nil {
            t.Errorf("failed to encode response: %v", err)
        }
    }))
    defer server.Close()

    result, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{
        Prompt: "a cat",
        N:      1,
        Size:   "1024x1024",
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if result == nil {
        t.Fatal("expected non-nil result")
    }

    if result.Created != 1719000000 {
        t.Errorf("expected Created 1719000000, got %d", result.Created)
    }

    if len(result.Data) != 1 {
        t.Fatalf("expected 1 data item, got %d", len(result.Data))
    }

    if result.Data[0].URL != "https://example.com/image.png" {
        t.Errorf("expected URL 'https://example.com/image.png', got %q", result.Data[0].URL)
    }

    if result.Data[0].RevisedPrompt == nil {
        t.Fatal("expected non-nil RevisedPrompt")
    }
    if *result.Data[0].RevisedPrompt != revised {
        t.Errorf("expected RevisedPrompt %q, got %q", revised, *result.Data[0].RevisedPrompt)
    }
}

func TestSendGeneration_MultipleImages(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := GenerationResponse{
            Created: 1719000001,
            Data: []GenerationData{
                {URL: "https://example.com/img1.png"},
                {URL: "https://example.com/img2.png"},
            },
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        if err := json.NewEncoder(w).Encode(resp); err != nil {
            t.Errorf("failed to encode response: %v", err)
        }
    }))
    defer server.Close()

    result, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{
        Prompt: "two dogs",
        N:      2,
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if len(result.Data) != 2 {
        t.Fatalf("expected 2 data items, got %d", len(result.Data))
    }
    if result.Data[0].URL != "https://example.com/img1.png" {
        t.Errorf("expected first URL 'https://example.com/img1.png', got %q", result.Data[0].URL)
    }
    if result.Data[1].URL != "https://example.com/img2.png" {
        t.Errorf("expected second URL 'https://example.com/img2.png', got %q", result.Data[1].URL)
    }
}

func TestSendGeneration_Error(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        errResp := GenerationError{}
        errResp.Error.Message = "Internal server error"
        errResp.Error.Type = "server_error"
        errResp.Error.Code = "internal_error"
        if err := json.NewEncoder(w).Encode(errResp); err != nil {
            t.Errorf("failed to encode error response: %v", err)
        }
    }))
    defer server.Close()

    _, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{Prompt: "test"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }

    if !strings.Contains(err.Error(), "server_error") {
        t.Errorf("expected error to contain 'server_error', got %q", err.Error())
    }
    if !strings.Contains(err.Error(), "Internal server error") {
        t.Errorf("expected error to contain 'Internal server error', got %q", err.Error())
    }
    if !strings.Contains(err.Error(), "500") {
        t.Errorf("expected error to contain '500', got %q", err.Error())
    }
}

func TestSendGeneration_ErrorWithoutBody(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusBadGateway)
    }))
    defer server.Close()

    _, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{Prompt: "test"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }

    if !strings.Contains(err.Error(), "502") {
        t.Errorf("expected error to contain '502', got %q", err.Error())
    }
}

func TestSendGeneration_InvalidJSON(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{invalid json}`))
    }))
    defer server.Close()

    _, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{Prompt: "test"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }
}

func TestSendGeneration_Unreachable(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        t.Error("server should not have been reached")
    }))
    server.Close()

    _, err := SendGeneration(http.DefaultClient, server.URL, &GenerationRequest{Prompt: "test"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }
}

func TestSendGeneration_TrailingSlash(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := GenerationResponse{
            Created: 1719000002,
            Data: []GenerationData{
                {URL: "https://example.com/image.png"},
            },
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        if err := json.NewEncoder(w).Encode(resp); err != nil {
            t.Errorf("failed to encode response: %v", err)
        }
    }))
    defer server.Close()

    result, err := SendGeneration(server.Client(), server.URL+"/", &GenerationRequest{Prompt: "test"})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if result == nil {
        t.Fatal("expected non-nil result")
    }
    if result.Created != 1719000002 {
        t.Errorf("expected Created 1719000002, got %d", result.Created)
    }
    if len(result.Data) != 1 {
        t.Fatalf("expected 1 data item, got %d", len(result.Data))
    }
    if result.Data[0].URL != "https://example.com/image.png" {
        t.Errorf("expected URL 'https://example.com/image.png', got %q", result.Data[0].URL)
    }
}

func TestSendGeneration_B64JSON(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := GenerationResponse{
            Created: 1719000003,
            Data: []GenerationData{
                {B64JSON: "c29tZSBpbWFnZSBkYXRh"},
            },
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        if err := json.NewEncoder(w).Encode(resp); err != nil {
            t.Errorf("failed to encode response: %v", err)
        }
    }))
    defer server.Close()

    result, err := SendGeneration(server.Client(), server.URL, &GenerationRequest{
        Prompt:         "test",
        ResponseFormat: "b64_json",
    })
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if result.Data[0].B64JSON != "c29tZSBpbWFnZSBkYXRh" {
        t.Errorf("expected B64JSON 'c29tZSBpbWFnZSBkYXRh', got %q", result.Data[0].B64JSON)
    }
}

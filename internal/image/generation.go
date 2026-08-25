package image

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
)

// GenerationRequest is the request body for an OpenAI-compatible image generation call.
type GenerationRequest struct {
    Model          string `json:"model,omitempty"`
    Prompt         string `json:"prompt"`
    N              int    `json:"n,omitempty"`
    Size           string `json:"size,omitempty"`
    Quality        string `json:"quality,omitempty"`
    Style          string `json:"style,omitempty"`
    ResponseFormat string `json:"response_format,omitempty"`
}

// GenerationData represents a single generated image in the response.
type GenerationData struct {
    URL           string  `json:"url,omitempty"`
    B64JSON       string  `json:"b64_json,omitempty"`
    RevisedPrompt *string `json:"revised_prompt,omitempty"`
}

// GenerationResponse is the response from an image generation API.
type GenerationResponse struct {
    Created int64            `json:"created"`
    Data    []GenerationData `json:"data"`
}

// GenerationError is the OpenAI-compatible error envelope.
type GenerationError struct {
    Error struct {
        Message string `json:"message"`
        Type    string `json:"type"`
        Code    string `json:"code"`
    } `json:"error"`
}

// SendGeneration POSTs a generation request to an image backend and returns the
// response. baseURL should be the root URL (e.g. "http://127.0.0.1:8080/v1").
// The request is sent to {baseURL}/images/generations. If apiKey is non-empty,
// the request carries "Authorization: Bearer <apiKey>".
func SendGeneration(client *http.Client, baseURL, apiKey string, req *GenerationRequest) (*GenerationResponse, error) {
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("image gen: marshal request: %w", err)
    }

    url := baseURL + "/images/generations"
    httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("image gen: create request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    if apiKey != "" {
        httpReq.Header.Set("Authorization", "Bearer "+apiKey)
    }

    resp, err := client.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("image gen: %w", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("image gen: read response: %w", err)
    }

    if resp.StatusCode >= 300 {
        var genErr GenerationError
        if err := json.Unmarshal(respBody, &genErr); err == nil && genErr.Error.Message != "" {
            return nil, fmt.Errorf("image gen: %s (status %d): %s", genErr.Error.Type, resp.StatusCode, genErr.Error.Message)
        }
        return nil, fmt.Errorf("image gen: backend returned status %d: %s", resp.StatusCode, string(respBody))
    }

    var genResp GenerationResponse
    if err := json.Unmarshal(respBody, &genResp); err != nil {
        return nil, fmt.Errorf("image gen: decode response: %w", err)
    }

    return &genResp, nil
}

package documents

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
	"llm-go-proxy/internal/scheduler"
)

// Coordinator handles fan-out of document chunks as low-priority
// KindDocument scheduler jobs and combines the results.
type Coordinator struct {
	sched  *scheduler.Scheduler
	logger *logging.Logger
}

// NewCoordinator creates a document inference coordinator.
func NewCoordinator(sched *scheduler.Scheduler, logger *logging.Logger) *Coordinator {
	return &Coordinator{
		sched:  sched,
		logger: logger,
	}
}

// Process submits all chunks as individual KindDocument jobs, waits for all
// results, and combines them into a ProcessResponse.
func (c *Coordinator) Process(
	requestID string,
	model string,
	chunks []Chunk,
	candidates []string,
	aliasConfig *config.AliasConfig,
	options *ollama.ChatOptions,
	ctx context.Context,
) (*ProcessResponse, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("document inference: no chunks to process")
	}

	total := len(chunks)

	type jobEntry struct {
		job   *scheduler.Job
		index int
	}

	jobs := make([]jobEntry, 0, total)
	systemMsgTemplate := "You are processing part %d of %d parts of a document. Respond directly and comprehensively about the content."

	for i, chunk := range chunks {
		systemMsg := fmt.Sprintf(systemMsgTemplate, i+1, total)
		messages := buildChunkMessages(chunk, i, total, systemMsg)

		jobID, err := scheduler.NewJobID()
		if err != nil {
			return nil, fmt.Errorf("document inference: generating job ID: %w", err)
		}

		job := &scheduler.Job{
			ID:             jobID,
			Kind:           scheduler.KindDocument,
			Priority:       scheduler.KindDocument.Priority(),
			RequestedModel: model,
			Candidates:     candidates,
			AliasConfig:    aliasConfig,
			Messages:       messages,
			Options:        options,
			ResultChan:     make(chan scheduler.JobResult, 1),
			JobCtx:         ctx,
		}

		if err := c.sched.Submit(job); err != nil {
			return nil, fmt.Errorf("document inference: submitting chunk %d: %w", i, err)
		}

		jobs = append(jobs, jobEntry{job: job, index: i})
	}

	// Collect results.
	type chunkResult struct {
		index    int
		response *ollama.ChatResponse
		err      error
	}

	results := make([]chunkResult, 0, total)

	for _, entry := range jobs {
		select {
		case result := <-entry.job.ResultChan:
			results = append(results, chunkResult{
				index:    entry.index,
				response: result.Response,
				err:      result.Err,
			})
		case <-ctx.Done():
			return nil, fmt.Errorf("document inference: context cancelled: %w", ctx.Err())
		}
	}

	// Sort results by chunk index.
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	// Combine successful results.
	var combined strings.Builder
	totalInput := 0
	totalOutput := 0
	successCount := 0

	for _, r := range results {
		if r.err != nil {
			if c.logger != nil {
				c.logger.Error("document chunk failed",
					logging.String("request_id", requestID),
					logging.Int("chunk_index", r.index),
					logging.String("error", r.err.Error()),
				)
			}
			continue
		}
		if successCount > 0 {
			combined.WriteString("\n\n---\n\n")
		}
		combined.WriteString(r.response.Message.Content)
		totalInput += r.response.PromptEvalCount
		totalOutput += r.response.EvalCount
		successCount++
	}

	if successCount == 0 {
		return nil, fmt.Errorf("document inference: all %d chunks failed", total)
	}

	return &ProcessResponse{
		ID:      "docproc_" + requestID,
		Object:  "document.process",
		Model:   model,
		Content: combined.String(),
		Pages:   len(chunks),
		Chunks:  len(chunks),
		Mode:    "chunk_inference",
		Method:  "chunk_inference",
		Usage: map[string]int{
			"input_tokens":  totalInput,
			"output_tokens": totalOutput,
			"total_tokens":  totalInput + totalOutput,
		},
	}, nil
}

// buildChunkMessages creates messages for a single chunk inference job.
func buildChunkMessages(chunk Chunk, index, total int, systemMsg string) []ollama.ChatMessage {
	messages := []ollama.ChatMessage{
		{Role: "system", Content: systemMsg},
	}

	switch chunk.Type {
	case ChunkTypeImage:
		img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(chunk.ImageData)
		messages = append(messages, ollama.ChatMessage{
			Role:    "user",
			Content: "Describe this page in detail.",
			Images:  []string{img},
		})
	default:
		messages = append(messages, ollama.ChatMessage{
			Role:    "user",
			Content: chunk.Text,
		})
	}

	return messages
}

package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const defaultQueryInstruction = "Given a creator-content platform question, retrieve Chinese evidence passages that answer the query"

const (
	maxQueryEmbeddingAttempts    = 3
	maxDocumentEmbeddingAttempts = 8
	embeddingRetryBaseDelay      = 100 * time.Millisecond
	embeddingRetryMaxDelay       = 5 * time.Second
)

type Embedder interface {
	EmbedDocuments(context.Context, []string) ([][]float32, error)
	EmbedQuery(context.Context, string) ([]float32, error)
}

type TEIEmbedder struct {
	baseURL          string
	model            string
	revision         string
	dimension        int
	instruction      string
	client           *http.Client
	queryAttempts    int
	documentAttempts int
	retryBaseDelay   time.Duration
	retryMaxDelay    time.Duration
}

func NewTEIEmbedder(baseURL string, model string, revision string, dimension int, timeout time.Duration) *TEIEmbedder {
	return &TEIEmbedder{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), model: model,
		revision: revision, dimension: dimension, instruction: defaultQueryInstruction,
		client: &http.Client{
			Timeout: timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport,
				otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
					return "tei " + request.Method
				}),
			),
		}, queryAttempts: maxQueryEmbeddingAttempts,
		documentAttempts: maxDocumentEmbeddingAttempts, retryBaseDelay: embeddingRetryBaseDelay,
		retryMaxDelay: embeddingRetryMaxDelay,
	}
}

func (e *TEIEmbedder) EmbedDocuments(ctx context.Context, inputs []string) (vectors [][]float32, err error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	finish := observability.StartRetrievalDependency("tei", "embed_documents")
	observability.ObserveRetrievalEmbeddingBatch("documents", len(inputs))
	defer func() { finish(retrievalDependencyResult(ctx, err)) }()
	return e.embed(ctx, inputs, "embed_documents", e.documentAttempts)
}

func (e *TEIEmbedder) EmbedQuery(ctx context.Context, query string) (vector []float32, err error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: embedding query is required", ErrInvalidInput)
	}
	finish := observability.StartRetrievalDependency("tei", "embed_query")
	observability.ObserveRetrievalEmbeddingBatch("query", 1)
	defer func() { finish(retrievalDependencyResult(ctx, err)) }()
	vectors, err := e.embed(ctx, []string{"Instruct: " + e.instruction + "\nQuery: " + query}, "embed_query", e.queryAttempts)
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (e *TEIEmbedder) embed(ctx context.Context, inputs []string, operation string, maxAttempts int) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	for _, input := range inputs {
		if strings.TrimSpace(input) == "" {
			return nil, fmt.Errorf("%w: embedding input cannot be empty", ErrInvalidInput)
		}
	}
	body, err := json.Marshal(map[string]any{"inputs": inputs, "truncate": true})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	var responseBody []byte
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embed", bytes.NewReader(body))
		if requestErr != nil {
			return nil, fmt.Errorf("create embedding request: %w", requestErr)
		}
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := e.client.Do(request)
		if requestErr != nil {
			if attempt < maxAttempts && waitForEmbeddingRetry(ctx, attempt, e.retryBaseDelay, e.retryMaxDelay) == nil {
				observability.IncRetrievalDependencyRetry("tei", operation, "transport")
				continue
			}
			return nil, fmt.Errorf("call embedding service: %w", requestErr)
		}
		limit := int64(64<<20) + 1
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			limit = 4096
		}
		responseBody, requestErr = io.ReadAll(io.LimitReader(response.Body, limit))
		closeErr := response.Body.Close()
		if requestErr != nil {
			return nil, fmt.Errorf("read embedding response: %w", requestErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close embedding response: %w", closeErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			break
		}
		if isRetryableEmbeddingStatus(response.StatusCode) && attempt < maxAttempts {
			observability.IncRetrievalDependencyRetry("tei", operation, fmt.Sprintf("http_%d", response.StatusCode))
			if err := waitForEmbeddingRetry(ctx, attempt, e.retryBaseDelay, e.retryMaxDelay); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("embedding service returned %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if len(responseBody) > 64<<20 {
		return nil, fmt.Errorf("embedding response exceeds 64 MiB")
	}
	var vectors [][]float32
	if err := json.Unmarshal(responseBody, &vectors); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(vectors) != len(inputs) {
		return nil, fmt.Errorf("embedding response count %d does not match input count %d", len(vectors), len(inputs))
	}
	for index, vector := range vectors {
		if len(vector) != e.dimension {
			return nil, fmt.Errorf("embedding %d has dimension %d, want %d for %s@%s", index, len(vector), e.dimension, e.model, e.revision)
		}
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("embedding %d contains a non-finite value", index)
			}
		}
	}
	return vectors, nil
}

func retrievalDependencyResult(ctx context.Context, err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "error"
}

func isRetryableEmbeddingStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func waitForEmbeddingRetry(ctx context.Context, attempt int, baseDelay time.Duration, maxDelay time.Duration) error {
	delay := baseDelay * time.Duration(1<<min(attempt-1, 20))
	if delay > maxDelay {
		delay = maxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("embedding retry canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

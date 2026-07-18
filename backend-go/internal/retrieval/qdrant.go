package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type VectorPoint struct {
	ID      int64
	Vector  []float32
	Payload map[string]any
}

type VectorHit struct {
	ID      int64
	Score   float64
	Payload map[string]any
}

type VectorStore interface {
	RecreateCollection(context.Context, string, int, map[string]any) error
	CreatePayloadIndex(context.Context, string, string, string) error
	Upsert(context.Context, string, []VectorPoint) error
	Query(context.Context, string, []float32, map[string]any, int) ([]VectorHit, error)
	Count(context.Context, string) (int64, error)
}

type QdrantClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewQdrantClient(baseURL string, apiKey string, timeout time.Duration) *QdrantClient {
	return &QdrantClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), apiKey: strings.TrimSpace(apiKey),
		client: &http.Client{Timeout: timeout},
	}
}

func (q *QdrantClient) RecreateCollection(ctx context.Context, collection string, dimension int, metadata map[string]any) error {
	path := "/collections/" + url.PathEscape(collection)
	if err := q.do(ctx, http.MethodDelete, path, nil, nil, http.StatusNotFound); err != nil {
		return fmt.Errorf("delete incomplete Qdrant collection: %w", err)
	}
	body := map[string]any{
		"vectors":         map[string]any{"size": dimension, "distance": "Cosine", "on_disk": true},
		"on_disk_payload": true,
		"metadata":        metadata,
	}
	if err := q.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("create Qdrant collection: %w", err)
	}
	return nil
}

func (q *QdrantClient) CreatePayloadIndex(ctx context.Context, collection string, field string, schema string) error {
	body := map[string]any{"field_name": field, "field_schema": schema}
	path := "/collections/" + url.PathEscape(collection) + "/index?wait=true"
	if err := q.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("create Qdrant payload index %s: %w", field, err)
	}
	return nil
}

func (q *QdrantClient) Upsert(ctx context.Context, collection string, points []VectorPoint) error {
	encoded := make([]map[string]any, 0, len(points))
	for _, point := range points {
		encoded = append(encoded, map[string]any{"id": point.ID, "vector": point.Vector, "payload": point.Payload})
	}
	path := "/collections/" + url.PathEscape(collection) + "/points?wait=true"
	if err := q.do(ctx, http.MethodPut, path, map[string]any{"points": encoded}, nil); err != nil {
		return fmt.Errorf("upsert Qdrant points: %w", err)
	}
	return nil
}

func (q *QdrantClient) Query(ctx context.Context, collection string, vector []float32, filter map[string]any, limit int) ([]VectorHit, error) {
	body := map[string]any{"query": vector, "filter": filter, "limit": limit, "with_payload": true, "with_vector": false}
	var envelope struct {
		Result struct {
			Points []struct {
				ID      json.Number    `json:"id"`
				Score   float64        `json:"score"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	path := "/collections/" + url.PathEscape(collection) + "/points/query"
	if err := q.do(ctx, http.MethodPost, path, body, &envelope); err != nil {
		return nil, fmt.Errorf("query Qdrant points: %w", err)
	}
	hits := make([]VectorHit, 0, len(envelope.Result.Points))
	for _, point := range envelope.Result.Points {
		id, err := point.ID.Int64()
		if err != nil {
			return nil, fmt.Errorf("decode Qdrant point id %q: %w", point.ID, err)
		}
		hits = append(hits, VectorHit{ID: id, Score: point.Score, Payload: point.Payload})
	}
	return hits, nil
}

func (q *QdrantClient) Count(ctx context.Context, collection string) (int64, error) {
	var envelope struct {
		Result struct {
			Count int64 `json:"count"`
		} `json:"result"`
	}
	path := "/collections/" + url.PathEscape(collection) + "/points/count"
	if err := q.do(ctx, http.MethodPost, path, map[string]any{"exact": true}, &envelope); err != nil {
		return 0, fmt.Errorf("count Qdrant points: %w", err)
	}
	return envelope.Result.Count, nil
}

func (q *QdrantClient) do(ctx context.Context, method string, path string, body any, output any, allowedStatus ...int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Qdrant request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, q.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create Qdrant request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if q.apiKey != "" {
		request.Header.Set("api-key", q.apiKey)
	}
	response, err := q.client.Do(request)
	if err != nil {
		return fmt.Errorf("call Qdrant: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		for _, status := range allowedStatus {
			if response.StatusCode == status {
				return nil
			}
		}
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Qdrant returned %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode Qdrant response: %w", err)
	}
	return nil
}

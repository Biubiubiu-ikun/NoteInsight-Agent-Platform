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
	CollectionExists(context.Context, string) (bool, error)
	CreatePayloadIndex(context.Context, string, string, string) error
	Upsert(context.Context, string, []VectorPoint) error
	Query(context.Context, string, []float32, map[string]any, int) ([]VectorHit, error)
	Count(context.Context, string) (int64, error)
	ListPointManifest(context.Context, string) ([]VectorManifestEntry, error)
	DeletePoints(context.Context, string, []int64) error
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

func (q *QdrantClient) CollectionExists(ctx context.Context, collection string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/collections/"+url.PathEscape(collection), nil)
	if err != nil {
		return false, fmt.Errorf("create Qdrant collection check: %w", err)
	}
	q.setHeaders(request)
	response, err := q.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("check Qdrant collection: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("check Qdrant collection: Qdrant returned %d", response.StatusCode)
	}
	return true, nil
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

func (q *QdrantClient) ListPointManifest(ctx context.Context, collection string) ([]VectorManifestEntry, error) {
	const pageSize = 1024
	points := make([]VectorManifestEntry, 0)
	var offset any
	for page := 0; ; page++ {
		if page > 1_000_000 {
			return nil, fmt.Errorf("scroll Qdrant point ids: pagination limit exceeded")
		}
		body := map[string]any{"limit": pageSize, "with_payload": []string{"content_hash"}, "with_vector": false}
		if offset != nil {
			body["offset"] = offset
		}
		var envelope struct {
			Result struct {
				Points []struct {
					ID      json.Number `json:"id"`
					Payload struct {
						ContentHash string `json:"content_hash"`
					} `json:"payload"`
				} `json:"points"`
				NextPageOffset json.RawMessage `json:"next_page_offset"`
			} `json:"result"`
		}
		path := "/collections/" + url.PathEscape(collection) + "/points/scroll"
		if err := q.do(ctx, http.MethodPost, path, body, &envelope); err != nil {
			return nil, fmt.Errorf("scroll Qdrant point ids: %w", err)
		}
		for _, point := range envelope.Result.Points {
			id, err := point.ID.Int64()
			if err != nil {
				return nil, fmt.Errorf("decode Qdrant point id %q: %w", point.ID, err)
			}
			points = append(points, VectorManifestEntry{ChunkID: id, ContentHash: point.Payload.ContentHash})
		}
		rawOffset := bytes.TrimSpace(envelope.Result.NextPageOffset)
		if len(rawOffset) == 0 || bytes.Equal(rawOffset, []byte("null")) {
			break
		}
		var nextOffset int64
		if err := json.Unmarshal(rawOffset, &nextOffset); err != nil {
			return nil, fmt.Errorf("decode Qdrant next page offset: %w", err)
		}
		if previous, ok := offset.(int64); ok && previous == nextOffset {
			return nil, fmt.Errorf("scroll Qdrant point ids: repeated offset %d", nextOffset)
		}
		offset = nextOffset
	}
	return points, nil
}

func (q *QdrantClient) DeletePoints(ctx context.Context, collection string, pointIDs []int64) error {
	if len(pointIDs) == 0 {
		return nil
	}
	path := "/collections/" + url.PathEscape(collection) + "/points/delete?wait=true"
	if err := q.do(ctx, http.MethodPost, path, map[string]any{"points": pointIDs}, nil); err != nil {
		return fmt.Errorf("delete Qdrant points: %w", err)
	}
	return nil
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
	q.setHeaders(request)
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

func (q *QdrantClient) setHeaders(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	if q.apiKey != "" {
		request.Header.Set("api-key", q.apiKey)
	}
}

// Package qdrant implements ports.VectorStore using the Qdrant HTTP REST API
// for embedding storage and cosine-similarity search.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// Store implements ports.VectorStore backed by a Qdrant instance.
type Store struct {
	endpoint   string // e.g. "http://localhost:6333"
	collection string // e.g. "mypal_memories"
	apiKey     string // optional API key for authentication
	client     *http.Client
}

// NewStore creates a new Qdrant-backed VectorStore.
func NewStore(endpoint, collection, apiKey string) *Store {
	if endpoint == "" {
		endpoint = "http://localhost:6333"
	}
	if collection == "" {
		collection = "mypal_memories"
	}
	return &Store{
		endpoint:   endpoint,
		collection: collection,
		apiKey:     apiKey,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Init creates the Qdrant collection if it does not already exist.
func (s *Store) Init(ctx context.Context, dimensions int) error {
	if dimensions <= 0 {
		return fmt.Errorf("qdrant: dimensions must be > 0, got %d", dimensions)
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     dimensions,
			"distance": "Cosine",
		},
	}

	resp, err := s.do(ctx, http.MethodPut, fmt.Sprintf("/collections/%s", s.collection), body)
	if err != nil {
		return fmt.Errorf("qdrant: init: %w", err)
	}
	defer resp.Body.Close()

	// 200 = created, 409 = already exists — both are fine.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: init: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Upsert inserts or updates a vector memory entry in Qdrant.
func (s *Store) Upsert(ctx context.Context, id, userID, content string, vector []float64, metadata map[string]any) error {
	payload := map[string]any{
		"content":    content,
		"user_id":    userID,
		"metadata":   metadata,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}

	body := map[string]any{
		"points": []map[string]any{
			{
				"id":      id,
				"vector":  vector,
				"payload": payload,
			},
		},
	}

	resp, err := s.do(ctx, http.MethodPut, fmt.Sprintf("/collections/%s/points", s.collection), body)
	if err != nil {
		return fmt.Errorf("qdrant: upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: upsert: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Search returns the top-K most similar entries for the given user.
func (s *Store) Search(ctx context.Context, vector []float64, userID string, topK int) ([]ports.VectorResult, error) {
	if topK <= 0 {
		topK = 10
	}

	body := map[string]any{
		"vector": vector,
		"limit":  topK,
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key":   "user_id",
					"match": map[string]any{"value": userID},
				},
			},
		},
		"with_payload": true,
	}

	resp, err := s.do(ctx, http.MethodPost, fmt.Sprintf("/collections/%s/points/search", s.collection), body)
	if err != nil {
		return nil, fmt.Errorf("qdrant: search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant: search: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("qdrant: search: decode response: %w", err)
	}

	var results []ports.VectorResult
	for _, hit := range result.Result {
		vr := ports.VectorResult{
			ID:    fmt.Sprintf("%v", hit.ID),
			Score: hit.Score,
		}
		if hit.Payload != nil {
			if v, ok := hit.Payload["content"].(string); ok {
				vr.Content = v
			}
			if v, ok := hit.Payload["user_id"].(string); ok {
				vr.UserID = v
			}
			if v, ok := hit.Payload["metadata"].(map[string]any); ok {
				vr.Metadata = v
			}
		}
		results = append(results, vr)
	}
	return results, nil
}

// Delete removes a vector memory entry by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	body := map[string]any{
		"points": []string{id},
	}

	resp, err := s.do(ctx, http.MethodPost, fmt.Sprintf("/collections/%s/points/delete", s.collection), body)
	if err != nil {
		return fmt.Errorf("qdrant: delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// DeleteByUser removes all vector memory entries for the given user.
func (s *Store) DeleteByUser(ctx context.Context, userID string) error {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key":   "user_id",
					"match": map[string]any{"value": userID},
				},
			},
		},
	}

	resp, err := s.do(ctx, http.MethodPost, fmt.Sprintf("/collections/%s/points/delete", s.collection), body)
	if err != nil {
		return fmt.Errorf("qdrant: delete by user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete by user: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// do sends a JSON HTTP request to the Qdrant API and returns the response.
func (s *Store) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.endpoint+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}

	return s.client.Do(req)
}

// searchResponse is the envelope returned by Qdrant's search endpoint.
type searchResponse struct {
	Result []searchHit `json:"result"`
}

type searchHit struct {
	ID      any            `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// Compile-time interface satisfaction check.
var _ ports.VectorStore = (*Store)(nil)

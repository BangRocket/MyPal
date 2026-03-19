package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultModel = "text-embedding-3-small"
const defaultDims = 1536

// Provider implements ports.EmbeddingProvider using the OpenAI embeddings API.
type Provider struct {
	apiKey string
	model  string
	dims   int
	client *http.Client
}

// NewProvider creates an OpenAI embedding provider. If model is empty,
// "text-embedding-3-small" is used (1536 dimensions).
func NewProvider(apiKey, model string) *Provider {
	if model == "" {
		model = defaultModel
	}
	dims := defaultDims
	// text-embedding-3-large uses 3072 dimensions.
	if model == "text-embedding-3-large" {
		dims = 3072
	}
	return &Provider{
		apiKey: apiKey,
		model:  model,
		dims:   dims,
		client: http.DefaultClient,
	}
}

// Dimensions returns the dimensionality of the embedding vectors.
func (p *Provider) Dimensions() int {
	return p.dims
}

// Embed returns the embedding vector for a single text input.
func (p *Provider) Embed(ctx context.Context, text string) ([]float64, error) {
	vecs, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai embeddings: empty response")
	}
	return vecs[0], nil
}

// embeddingRequest is the JSON body sent to the OpenAI embeddings API.
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingResponse represents the relevant fields of the OpenAI response.
type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// EmbedBatch returns embedding vectors for multiple text inputs in a single
// API call.
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embeddingRequest{
		Input: texts,
		Model: p.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embeddings: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result embeddingResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai embeddings: unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("openai embeddings: API error: %s", result.Error.Message)
	}

	vecs := make([][]float64, len(result.Data))
	for i, d := range result.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

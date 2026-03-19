package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	defaultEndpoint = "http://localhost:11434"
	defaultModel    = "nomic-embed-text"
	defaultDims     = 768
)

// Provider implements ports.EmbeddingProvider using the Ollama embeddings API.
type Provider struct {
	endpoint string
	model    string
	dims     int
	client   *http.Client
}

// NewProvider creates an Ollama embedding provider. If endpoint is empty,
// "http://localhost:11434" is used. If model is empty, "nomic-embed-text"
// is used (768 dimensions).
func NewProvider(endpoint, model string) *Provider {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		endpoint: endpoint,
		model:    model,
		dims:     defaultDims,
		client:   http.DefaultClient,
	}
}

// Dimensions returns the dimensionality of the embedding vectors.
func (p *Provider) Dimensions() int {
	return p.dims
}

// embeddingRequest is the JSON body sent to the Ollama embeddings API.
type embeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// embeddingResponse represents the relevant fields of the Ollama response.
type embeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed returns the embedding vector for a single text input.
func (p *Provider) Embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(embeddingRequest{
		Model:  p.model,
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embeddings: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result embeddingResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("ollama embeddings: unmarshal response: %w", err)
	}

	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embeddings: empty embedding in response")
	}

	return result.Embedding, nil
}

// EmbedBatch returns embedding vectors for multiple text inputs. Ollama does
// not support batch embeddings, so each text is embedded sequentially.
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	vecs := make([][]float64, len(texts))
	for i, text := range texts {
		vec, err := p.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("ollama embeddings: batch item %d: %w", i, err)
		}
		vecs[i] = vec
	}
	return vecs, nil
}

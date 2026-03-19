package ports

import "context"

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	// Embed returns the embedding vector for a single text input.
	Embed(ctx context.Context, text string) ([]float64, error)

	// EmbedBatch returns embedding vectors for multiple text inputs.
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)

	// Dimensions returns the dimensionality of the embedding vectors produced
	// by this provider.
	Dimensions() int
}

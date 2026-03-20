package ports

import "context"

// VectorStore defines the interface for vector-based memory storage backends
// (e.g. pgvector). It provides CRUD operations and similarity search over
// embedding vectors scoped by user.
type VectorStore interface {
	// Init creates the required extension and table for the given embedding
	// dimensionality. It is idempotent (uses IF NOT EXISTS).
	Init(ctx context.Context, dimensions int) error

	// Upsert inserts or updates a vector memory entry. The vector slice must
	// match the dimensionality configured during Init.
	Upsert(ctx context.Context, id, userID, content string, vector []float64, metadata map[string]any) error

	// Search returns the top-K most similar entries for the given user,
	// ordered by descending cosine similarity score.
	Search(ctx context.Context, vector []float64, userID string, topK int) ([]VectorResult, error)

	// Delete removes a vector memory entry by ID.
	Delete(ctx context.Context, id string) error

	// DeleteByUser removes all vector memory entries for the given user.
	DeleteByUser(ctx context.Context, userID string) error
}

// VectorResult represents a single result from a vector similarity search.
type VectorResult struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata"`
	UserID   string         `json:"user_id"`
}

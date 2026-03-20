package memory

import (
	"context"
	"fmt"
	"log"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/google/uuid"
)

const defaultTopK = 10

// VectorMemory provides a domain-level API over a VectorStore and
// EmbeddingProvider, handling embedding generation and ID assignment.
type VectorMemory struct {
	store    ports.VectorStore
	embedder ports.EmbeddingProvider
	topK     int
}

// NewVectorMemory creates a VectorMemory service. If topK <= 0 the default
// of 10 is used.
func NewVectorMemory(store ports.VectorStore, embedder ports.EmbeddingProvider, topK int) *VectorMemory {
	if topK <= 0 {
		topK = defaultTopK
	}
	return &VectorMemory{
		store:    store,
		embedder: embedder,
		topK:     topK,
	}
}

// Init initialises the underlying vector store for the embedder's
// dimensionality. It is idempotent.
func (vm *VectorMemory) Init(ctx context.Context) error {
	log.Printf("vector: initialized store (%d dimensions)", vm.embedder.Dimensions())
	return vm.store.Init(ctx, vm.embedder.Dimensions())
}

// Remember embeds content and upserts it into the vector store with a newly
// generated UUID.
func (vm *VectorMemory) Remember(ctx context.Context, userID, content string, metadata map[string]any) error {
	log.Printf("vector: storing memory for user %s (%d chars)", userID, len(content))
	id := uuid.New().String()

	vec, err := vm.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("vector memory: embed content: %w", err)
	}

	if err := vm.store.Upsert(ctx, id, userID, content, vec, metadata); err != nil {
		return fmt.Errorf("vector memory: upsert: %w", err)
	}

	return nil
}

// Recall embeds the query and searches the vector store for the most similar
// entries belonging to userID. If topK <= 0 the service default is used.
func (vm *VectorMemory) Recall(ctx context.Context, userID, query string, topK int) ([]ports.VectorResult, error) {
	if topK <= 0 {
		topK = vm.topK
	}

	q := query
	if len(q) > 50 {
		q = q[:50] + "..."
	}
	log.Printf("vector: searching for user %s query=%q top_k=%d", userID, q, topK)

	vec, err := vm.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("vector memory: embed query: %w", err)
	}

	results, err := vm.store.Search(ctx, vec, userID, topK)
	if err != nil {
		return nil, fmt.Errorf("vector memory: search: %w", err)
	}

	bestScore := 0.0
	for _, r := range results {
		if r.Score > bestScore {
			bestScore = r.Score
		}
	}
	log.Printf("vector: found %d results (best score=%.3f)", len(results), bestScore)

	return results, nil
}

// Forget removes a memory entry by ID from the vector store.
func (vm *VectorMemory) Forget(ctx context.Context, id string) error {
	log.Printf("vector: deleting memory %s", id)
	if err := vm.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("vector memory: delete: %w", err)
	}
	return nil
}

// ForgetAll removes all memory entries for a user from the vector store.
func (vm *VectorMemory) ForgetAll(ctx context.Context, userID string) error {
	if err := vm.store.DeleteByUser(ctx, userID); err != nil {
		return fmt.Errorf("vector memory: delete by user: %w", err)
	}
	return nil
}

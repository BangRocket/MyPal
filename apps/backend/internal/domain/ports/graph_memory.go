package ports

import (
	"context"
	"time"
)

// GraphBackend defines the interface for graph-based memory storage backends
// (e.g. FalkorDB, Kuzu). It provides entity/relation CRUD, neighbor traversal,
// text search, raw Cypher queries, and full-graph retrieval scoped by user.
type GraphBackend interface {
	AddEntity(ctx context.Context, entity GraphEntity) error
	AddRelation(ctx context.Context, rel GraphRelation) error
	GetEntity(ctx context.Context, id string) (*GraphEntity, error)
	GetNeighbors(ctx context.Context, entityID string, depth int) ([]GraphEntity, []GraphRelation, error)
	Search(ctx context.Context, userID, text string, limit int) ([]GraphEntity, error)
	DeleteEntity(ctx context.Context, id string) error
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
	UserGraph(ctx context.Context, userID string) ([]GraphEntity, []GraphRelation, error)
}

// GraphEntity represents a node in the graph memory store.
type GraphEntity struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	UserID     string         `json:"user_id"`
	Properties map[string]any `json:"properties"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// GraphRelation represents a directed edge between two entities in the graph.
type GraphRelation struct {
	ID       string         `json:"id"`
	FromID   string         `json:"from_id"`
	ToID     string         `json:"to_id"`
	Type     string         `json:"type"`
	Weight   float64        `json:"weight"`
	Metadata map[string]any `json:"metadata"`
}

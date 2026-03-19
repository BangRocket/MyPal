# Phase 4: Memory System Port

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Port the dual-layer memory system (vector search + graph) from MyPalClara's Python/mem0 to Go, with pluggable embedding providers (OpenAI/Ollama) and graph backends (FalkorDB/Kuzu).

**Architecture:** Unified `MemorySystem` interface wrapping `VectorMemory` (pgvector/Qdrant) and `GraphMemory` (FalkorDB/Kuzu). Pluggable `EmbeddingProvider` interface. Extends OpenLobster's existing Neo4j-based memory with vector search and richer graph model.

**Tech Stack:** Go, pgvector, FalkorDB (Redis protocol), Kuzu (embedded), OpenAI embeddings API, Ollama API, GORM, gqlgen, SolidJS

---

## Task 1: Embedding Provider Interface + OpenAI Implementation

**Files:**
- Create: `apps/backend/internal/domain/ports/embedding.go`
- Create: `apps/backend/internal/infrastructure/adapters/embedding/openai/provider.go`

Port interface:
```go
type EmbeddingProvider interface {
    Embed(ctx context.Context, text string) ([]float64, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)
    Dimensions() int
}
```

OpenAI implementation: call `POST /v1/embeddings` with model `text-embedding-3-small` (1536 dims). Use `net/http` + JSON directly — no heavy SDK needed.

Config: add `EmbeddingConfig` to config.go:
```go
type EmbeddingConfig struct {
    Provider string `mapstructure:"provider"` // "openai" or "ollama"
    OpenAI   struct {
        APIKey string `mapstructure:"api_key"`
        Model  string `mapstructure:"model"` // default "text-embedding-3-small"
    } `mapstructure:"openai"`
    Ollama struct {
        Endpoint string `mapstructure:"endpoint"` // default "http://localhost:11434"
        Model    string `mapstructure:"model"`     // default "nomic-embed-text"
    } `mapstructure:"ollama"`
}
```

Verify build. Commit.

---

## Task 2: Ollama Embedding Provider

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/embedding/ollama/provider.go`

Call Ollama's `/api/embeddings` endpoint. Model defaults to `nomic-embed-text` (768 dims).

Verify build. Commit.

---

## Task 3: Vector Memory with pgvector

**Files:**
- Create: `apps/backend/internal/domain/ports/vector_memory.go`
- Create: `apps/backend/internal/infrastructure/adapters/memory/pgvector/store.go`

Port:
```go
type VectorStore interface {
    Upsert(ctx context.Context, id string, vector []float64, content string, metadata map[string]any) error
    Search(ctx context.Context, vector []float64, topK int, filters map[string]any) ([]VectorResult, error)
    Delete(ctx context.Context, id string) error
}
```

pgvector implementation: use raw SQL via GORM's `db.Exec()` and `db.Raw()`.
- Table: `memory_vectors` (id TEXT PK, user_id TEXT, content TEXT, metadata JSONB, embedding vector(dims), created_at TIMESTAMP)
- Search: `SELECT *, 1 - (embedding <=> $1) AS score FROM memory_vectors WHERE user_id = $2 ORDER BY embedding <=> $1 LIMIT $3`
- Create table via raw SQL migration (pgvector needs `CREATE EXTENSION IF NOT EXISTS vector`)

Note: This only works with PostgreSQL. If the DB is SQLite, vector memory is disabled (log a warning).

Verify build. Commit.

---

## Task 4: Vector Memory Service

**Files:**
- Create: `apps/backend/internal/domain/services/memory/vector_memory.go`

```go
type VectorMemory struct {
    store     ports.VectorStore
    embedder  ports.EmbeddingProvider
    chunkSize int    // default 512 tokens
    topK      int    // default 10
}
```

Methods:
- `Remember(ctx, userID, content string, metadata map[string]any) error` — embed content, upsert to store
- `Recall(ctx, userID, query string, topK int) ([]VectorResult, error)` — embed query, search store
- `Forget(ctx, userID, id string) error` — delete from store

Verify build. Commit.

---

## Task 5: Graph Memory with FalkorDB

**Files:**
- Create: `apps/backend/internal/domain/ports/graph_memory.go`
- Create: `apps/backend/internal/infrastructure/adapters/memory/falkordb/store.go`

Port:
```go
type GraphBackend interface {
    AddEntity(ctx context.Context, entity GraphEntity) error
    AddRelation(ctx context.Context, rel GraphRelation) error
    GetEntity(ctx context.Context, id string) (*GraphEntity, error)
    GetNeighbors(ctx context.Context, entityID string, depth int) ([]GraphEntity, []GraphRelation, error)
    Search(ctx context.Context, userID, text string, limit int) ([]GraphEntity, error)
    DeleteEntity(ctx context.Context, id string) error
    Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

type GraphEntity struct {
    ID, Type, Name, UserID string
    Properties map[string]any
    CreatedAt, UpdatedAt time.Time
}

type GraphRelation struct {
    ID, FromID, ToID, Type string
    Weight float64
    Metadata map[string]any
}
```

FalkorDB implementation: use Redis protocol (`github.com/redis/go-redis/v9`) with `GRAPH.QUERY` command. FalkorDB is Redis-compatible and uses Cypher for graph queries.

Verify build. Commit.

---

## Task 6: Kuzu Graph Backend

**Files:**
- Create: `apps/backend/internal/infrastructure/adapters/memory/kuzu/store.go`

Kuzu is an embedded graph database. Use the Kuzu Go bindings if available, or implement via CLI exec (`kuzu` command) as fallback (similar to Docker/Incus pattern).

If Kuzu Go bindings don't exist or are too complex, implement a simple file-based graph store as the lightweight alternative (JSON file with adjacency lists).

Verify build. Commit.

---

## Task 7: Unified Memory System

**Files:**
- Create: `apps/backend/internal/domain/services/memory/memory_system.go`

```go
type MemorySystem struct {
    Vector *VectorMemory
    Graph  GraphBackend
}

func (m *MemorySystem) Remember(ctx, userID, content string, metadata MemoryMetadata) error
func (m *MemorySystem) Recall(ctx, userID, query string, opts RecallOptions) ([]Memory, error)
func (m *MemorySystem) Bootstrap(ctx, userID string, profile UserProfile) error
func (m *MemorySystem) Forget(ctx, userID string) error
```

`Remember`: embed content into vector store + extract entities/relations for graph store.
`Recall`: search vector store + get graph context for related entities, merge results.
`Bootstrap`: seed graph with profile entities (person, interests, projects, relationships).

Verify build. Commit.

---

## Task 8: Memory Configuration + Wiring

**Files:**
- Modify: `apps/backend/internal/infrastructure/config/config.go`
- Modify: `apps/backend/cmd/mypal/serve/services.go`

Add MemoryConfig with vector/graph/embedding sections. Wire the memory system creation on startup: create embedding provider → create vector store → create graph backend → create MemorySystem → integrate with existing memory tools.

Verify build. Commit.

---

## Task 9: Memory GraphQL Schema + Resolvers

**Files:**
- Modify: `schema/memory.graphql` (extend existing)
- Create resolver, wire deps

Add vector search query and graph exploration queries to the existing memory schema. The existing codebase already has memory GraphQL — extend it with vector search capabilities.

Verify build. Commit.

---

## Task 10: Frontend — Enhanced Memory View

**Files:**
- Modify existing memory view or create enhanced version

Add:
- Vector search: text input → search → show results with similarity scores
- Graph browser: entity detail view, neighbor exploration (leverage existing Cytoscape.js)
- Memory stats: total memories, entities, relations per user

Verify frontend builds. Commit.

---

## Summary

| Task | Description | Dependencies |
|------|-------------|-------------|
| 1 | Embedding port + OpenAI provider + config | None |
| 2 | Ollama embedding provider | Task 1 |
| 3 | Vector memory with pgvector | Task 1 |
| 4 | Vector memory service | Tasks 1, 3 |
| 5 | Graph memory with FalkorDB | None |
| 6 | Kuzu graph backend | None |
| 7 | Unified memory system | Tasks 4, 5 |
| 8 | Configuration + wiring | Task 7 |
| 9 | Memory GraphQL | Task 8 |
| 10 | Frontend enhanced memory | Task 9 |

// Package pgvector implements ports.VectorStore using PostgreSQL with the
// pgvector extension for embedding storage and cosine-similarity search.
package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"gorm.io/gorm"
)

// Store implements ports.VectorStore backed by PostgreSQL + pgvector.
type Store struct {
	db *gorm.DB
}

// NewStore creates a new pgvector-backed VectorStore. The caller must ensure
// the underlying GORM connection is to a PostgreSQL database with pgvector
// available.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Init creates the pgvector extension and the memory_vectors table. It is
// idempotent and safe to call on every application startup.
func (s *Store) Init(ctx context.Context, dimensions int) error {
	log.Printf("pgvector: initializing table (dims=%d)", dimensions)
	// Guard: pgvector only works on PostgreSQL.
	if !isPostgres(s.db) {
		return fmt.Errorf("pgvector: vector memory requires PostgreSQL (current driver: %s)", s.db.Dialector.Name())
	}

	if dimensions <= 0 {
		return fmt.Errorf("pgvector: dimensions must be > 0, got %d", dimensions)
	}

	stmts := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS memory_vectors (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL,
			content    TEXT NOT NULL,
			metadata   JSONB,
			embedding  vector(%d),
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`, dimensions),
		"CREATE INDEX IF NOT EXISTS idx_mv_user ON memory_vectors(user_id)",
	}

	for _, stmt := range stmts {
		if err := s.db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return fmt.Errorf("pgvector: init: %w", err)
		}
	}
	return nil
}

// Upsert inserts a new vector entry or updates an existing one (matched by id).
func (s *Store) Upsert(ctx context.Context, id, userID, content string, vector []float64, metadata map[string]any) error {
	log.Printf("pgvector: upsert id=%s user=%s", id, userID)
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("pgvector: marshal metadata: %w", err)
	}

	vecStr := formatVector(vector)

	sql := `INSERT INTO memory_vectors (id, user_id, content, metadata, embedding)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE
		SET content = EXCLUDED.content, metadata = EXCLUDED.metadata, embedding = EXCLUDED.embedding`

	if err := s.db.WithContext(ctx).Exec(sql, id, userID, content, string(metaJSON), vecStr).Error; err != nil {
		return fmt.Errorf("pgvector: upsert: %w", err)
	}
	return nil
}

// Search returns the top-K most similar entries for the given user, ordered by
// descending cosine similarity (score = 1 - cosine distance).
func (s *Store) Search(ctx context.Context, vector []float64, userID string, topK int) ([]ports.VectorResult, error) {
	log.Printf("pgvector: search top_k=%d user=%s", topK, userID)
	if topK <= 0 {
		topK = 10
	}

	vecStr := formatVector(vector)

	sql := `SELECT id, user_id, content, metadata, 1 - (embedding <=> $1) AS score
		FROM memory_vectors
		WHERE user_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3`

	rows, err := s.db.WithContext(ctx).Raw(sql, vecStr, userID, topK).Rows()
	if err != nil {
		return nil, fmt.Errorf("pgvector: search: %w", err)
	}
	defer rows.Close()

	var results []ports.VectorResult
	for rows.Next() {
		var (
			r       ports.VectorResult
			metaRaw []byte
		)
		if err := rows.Scan(&r.ID, &r.UserID, &r.Content, &metaRaw, &r.Score); err != nil {
			return nil, fmt.Errorf("pgvector: search scan: %w", err)
		}
		if len(metaRaw) > 0 {
			_ = json.Unmarshal(metaRaw, &r.Metadata)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgvector: search rows: %w", err)
	}
	log.Printf("pgvector: returned %d rows", len(results))
	return results, nil
}

// Delete removes a vector memory entry by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := s.db.WithContext(ctx).Exec("DELETE FROM memory_vectors WHERE id = $1", id).Error; err != nil {
		return fmt.Errorf("pgvector: delete: %w", err)
	}
	return nil
}

// DeleteByUser removes all vector memory entries for the given user.
func (s *Store) DeleteByUser(ctx context.Context, userID string) error {
	if err := s.db.WithContext(ctx).Exec("DELETE FROM memory_vectors WHERE user_id = $1", userID).Error; err != nil {
		return fmt.Errorf("pgvector: delete by user: %w", err)
	}
	return nil
}

// formatVector converts a float64 slice to the pgvector text format: [0.1,0.2,0.3]
func formatVector(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%v", f)
	}
	b.WriteByte(']')
	return b.String()
}

// isPostgres checks whether the GORM dialector is PostgreSQL.
func isPostgres(db *gorm.DB) bool {
	name := db.Dialector.Name()
	return name == "postgres" || name == "pgx"
}

// Compile-time interface satisfaction check.
var _ ports.VectorStore = (*Store)(nil)

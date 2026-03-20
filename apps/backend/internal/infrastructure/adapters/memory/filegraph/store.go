package filegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// fileData is the JSON structure persisted to disk.
type fileData struct {
	Entities  map[string]ports.GraphEntity  `json:"entities"`
	Relations map[string]ports.GraphRelation `json:"relations"`
}

// Store is a JSON-file-backed graph that implements ports.GraphBackend.
// On creation it loads from file; on every write it saves atomically
// (write to temp file, rename). Suitable for small/test deployments.
type Store struct {
	path      string
	entities  map[string]ports.GraphEntity
	relations map[string]ports.GraphRelation
	mu        sync.RWMutex
}

// NewStore creates a new file-backed graph store. If the file at path exists,
// its contents are loaded into memory. If it does not exist, an empty graph is
// initialised and persisted.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path:      path,
		entities:  make(map[string]ports.GraphEntity),
		relations: make(map[string]ports.GraphRelation),
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("filegraph: read %s: %w", path, err)
	}

	if err == nil && len(data) > 0 {
		var fd fileData
		if err := json.Unmarshal(data, &fd); err != nil {
			return nil, fmt.Errorf("filegraph: parse %s: %w", path, err)
		}
		if fd.Entities != nil {
			s.entities = fd.Entities
		}
		if fd.Relations != nil {
			s.relations = fd.Relations
		}
	}

	return s, nil
}

// save persists the current in-memory state to disk atomically.
// Caller must hold s.mu write lock.
func (s *Store) save() error {
	fd := fileData{
		Entities:  s.entities,
		Relations: s.relations,
	}
	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return fmt.Errorf("filegraph: marshal: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("filegraph: mkdir %s: %w", dir, err)
	}

	// Atomic write: write to temp file in the same directory, then rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("filegraph: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("filegraph: rename: %w", err)
	}
	return nil
}

// AddEntity adds an entity to the graph and persists to disk.
func (s *Store) AddEntity(_ context.Context, entity ports.GraphEntity) error {
	log.Printf("filegraph: add entity %s type=%s", entity.ID, entity.Type)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entities[entity.ID] = entity
	return s.save()
}

// AddRelation adds a relation to the graph and persists to disk.
func (s *Store) AddRelation(_ context.Context, rel ports.GraphRelation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.relations[rel.ID] = rel
	return s.save()
}

// GetEntity retrieves a single entity by ID.
func (s *Store) GetEntity(_ context.Context, id string) (*ports.GraphEntity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.entities[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return &e, nil
}

// GetNeighbors returns entities and relations connected to the given entity ID,
// traversing up to depth hops via breadth-first search.
func (s *Store) GetNeighbors(_ context.Context, entityID string, depth int) ([]ports.GraphEntity, []ports.GraphRelation, error) {
	log.Printf("filegraph: get neighbors of %s depth=%d", entityID, depth)
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.entities[entityID]; !ok {
		return nil, nil, ports.ErrNotFound
	}

	if depth <= 0 {
		depth = 1
	}

	visited := map[string]bool{entityID: true}
	frontier := []string{entityID}
	var collectedRelations []ports.GraphRelation

	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, current := range frontier {
			for _, rel := range s.relations {
				var neighbor string
				if rel.FromID == current {
					neighbor = rel.ToID
				} else if rel.ToID == current {
					neighbor = rel.FromID
				} else {
					continue
				}
				collectedRelations = append(collectedRelations, rel)
				if !visited[neighbor] {
					visited[neighbor] = true
					next = append(next, neighbor)
				}
			}
		}
		frontier = next
	}

	// Collect all visited entities except the starting node.
	var entities []ports.GraphEntity
	for vid := range visited {
		if vid == entityID {
			continue
		}
		if e, ok := s.entities[vid]; ok {
			entities = append(entities, e)
		}
	}
	return entities, collectedRelations, nil
}

// Search finds entities where UserID matches userID and Name contains query
// text (case-insensitive). Returns up to limit results.
func (s *Store) Search(_ context.Context, userID, text string, limit int) ([]ports.GraphEntity, error) {
	q := text
	if len(q) > 50 {
		q = q[:50] + "..."
	}
	log.Printf("filegraph: search user=%s query=%q", userID, q)
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	textLower := strings.ToLower(text)
	var results []ports.GraphEntity

	for _, e := range s.entities {
		if e.UserID != userID {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(e.Name), textLower) {
			continue
		}
		results = append(results, e)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// DeleteEntity removes an entity and all relations that reference it.
func (s *Store) DeleteEntity(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entities[id]; !ok {
		return ports.ErrNotFound
	}

	delete(s.entities, id)

	// Remove all relations referencing this entity.
	for rid, rel := range s.relations {
		if rel.FromID == id || rel.ToID == id {
			delete(s.relations, rid)
		}
	}

	return s.save()
}

// Query returns an error because Cypher queries are not supported in the
// file-based backend.
func (s *Store) Query(_ context.Context, _ string, _ map[string]any) ([]map[string]any, error) {
	return nil, fmt.Errorf("filegraph: cypher queries not supported in file backend")
}

// UserGraph returns all entities and relations belonging to the given user.
// Entities are matched by UserID. Relations are included when both endpoints
// belong to the user's entity set.
func (s *Store) UserGraph(_ context.Context, userID string) ([]ports.GraphEntity, []ports.GraphRelation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entities []ports.GraphEntity
	entityIDs := make(map[string]bool)

	for _, e := range s.entities {
		if e.UserID == userID {
			entities = append(entities, e)
			entityIDs[e.ID] = true
		}
	}

	var relations []ports.GraphRelation
	for _, rel := range s.relations {
		if entityIDs[rel.FromID] || entityIDs[rel.ToID] {
			relations = append(relations, rel)
		}
	}

	return entities, relations, nil
}

// Compile-time interface check.
var _ ports.GraphBackend = (*Store)(nil)

// Package falkordb implements the ports.GraphBackend interface using FalkorDB,
// a graph database that speaks the Redis protocol with GRAPH.QUERY for Cypher.
//
// FalkorDB's GRAPH.QUERY response format is complex to parse from raw RESP.
// This implementation uses a hybrid approach: graph operations are expressed as
// Cypher queries sent via GRAPH.QUERY, while entity/relation metadata is also
// mirrored in Redis hash/set structures for reliable reads. Writes go through
// Cypher (so FalkorDB maintains a queryable graph), and reads that need
// structured data use the mirrored hashes.
package falkordb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
)

// Store implements ports.GraphBackend backed by FalkorDB (Redis protocol + GRAPH.QUERY).
type Store struct {
	client    *redis.Client
	graphName string
}

// NewStore creates a FalkorDB-backed graph store. graphName defaults to
// "mypal_memory" when empty.
func NewStore(addr, password, graphName string) (*Store, error) {
	if graphName == "" {
		graphName = "mypal_memory"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("falkordb: unable to connect to %s: %w", addr, err)
	}

	return &Store{
		client:    client,
		graphName: graphName,
	}, nil
}

// Close releases the underlying Redis connection.
func (s *Store) Close() error {
	return s.client.Close()
}

// ---------------------------------------------------------------------
// Redis mirror keys
// ---------------------------------------------------------------------
//
// Each entity is stored as:
//   HSET entity:<id>  id, type, name, user_id, properties_json, created_at, updated_at
//
// Per-user index:
//   SADD user_entities:<user_id>  <entity_id>
//
// Each relation is stored as:
//   HSET relation:<id>  id, from_id, to_id, type, weight, metadata_json
//
// Neighbor index (bidirectional):
//   SADD neighbors:<entity_id>  <relation_id>

func entityKey(id string) string          { return "entity:" + id }
func relationKey(id string) string         { return "relation:" + id }
func userEntitiesKey(userID string) string { return "user_entities:" + userID }
func neighborsKey(entityID string) string  { return "neighbors:" + entityID }

// ---------------------------------------------------------------------
// GraphBackend implementation
// ---------------------------------------------------------------------

// AddEntity creates a node in FalkorDB and mirrors it to Redis hashes.
func (s *Store) AddEntity(ctx context.Context, entity ports.GraphEntity) error {
	if entity.ID == "" {
		entity.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if entity.CreatedAt.IsZero() {
		entity.CreatedAt = now
	}
	entity.UpdatedAt = now

	// Cypher: create the node in the graph.
	cypher := fmt.Sprintf(
		"CREATE (n:%s {id: '%s', name: '%s', user_id: '%s', created_at: '%s', updated_at: '%s'})",
		sanitizeLabel(entity.Type),
		escCypher(entity.ID),
		escCypher(entity.Name),
		escCypher(entity.UserID),
		entity.CreatedAt.Format(time.RFC3339),
		entity.UpdatedAt.Format(time.RFC3339),
	)
	if err := s.graphQuery(ctx, cypher); err != nil {
		return fmt.Errorf("falkordb: AddEntity graph query: %w", err)
	}

	// Mirror to Redis hash for reliable reads.
	propsJSON, _ := json.Marshal(entity.Properties)
	if err := s.client.HSet(ctx, entityKey(entity.ID), map[string]interface{}{
		"id":              entity.ID,
		"type":            entity.Type,
		"name":            entity.Name,
		"user_id":         entity.UserID,
		"properties_json": string(propsJSON),
		"created_at":      entity.CreatedAt.Format(time.RFC3339),
		"updated_at":      entity.UpdatedAt.Format(time.RFC3339),
	}).Err(); err != nil {
		return fmt.Errorf("falkordb: AddEntity mirror: %w", err)
	}

	// User index.
	if entity.UserID != "" {
		s.client.SAdd(ctx, userEntitiesKey(entity.UserID), entity.ID)
	}

	return nil
}

// AddRelation creates a relationship in FalkorDB and mirrors it to Redis.
func (s *Store) AddRelation(ctx context.Context, rel ports.GraphRelation) error {
	if rel.ID == "" {
		rel.ID = uuid.New().String()
	}

	cypher := fmt.Sprintf(
		"MATCH (a {id: '%s'}), (b {id: '%s'}) CREATE (a)-[r:%s {id: '%s', weight: %f}]->(b)",
		escCypher(rel.FromID),
		escCypher(rel.ToID),
		sanitizeLabel(rel.Type),
		escCypher(rel.ID),
		rel.Weight,
	)
	if err := s.graphQuery(ctx, cypher); err != nil {
		return fmt.Errorf("falkordb: AddRelation graph query: %w", err)
	}

	metaJSON, _ := json.Marshal(rel.Metadata)
	if err := s.client.HSet(ctx, relationKey(rel.ID), map[string]interface{}{
		"id":            rel.ID,
		"from_id":       rel.FromID,
		"to_id":         rel.ToID,
		"type":          rel.Type,
		"weight":        fmt.Sprintf("%f", rel.Weight),
		"metadata_json": string(metaJSON),
	}).Err(); err != nil {
		return fmt.Errorf("falkordb: AddRelation mirror: %w", err)
	}

	// Bidirectional neighbor index.
	s.client.SAdd(ctx, neighborsKey(rel.FromID), rel.ID)
	s.client.SAdd(ctx, neighborsKey(rel.ToID), rel.ID)

	return nil
}

// GetEntity retrieves a single entity by ID from the Redis mirror.
func (s *Store) GetEntity(ctx context.Context, id string) (*ports.GraphEntity, error) {
	vals, err := s.client.HGetAll(ctx, entityKey(id)).Result()
	if err != nil {
		return nil, fmt.Errorf("falkordb: GetEntity: %w", err)
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("falkordb: entity %q not found", id)
	}
	return hashToEntity(vals), nil
}

// GetNeighbors returns entities and relations within `depth` hops of entityID.
// Depth is implemented iteratively via the neighbor index.
func (s *Store) GetNeighbors(ctx context.Context, entityID string, depth int) ([]ports.GraphEntity, []ports.GraphRelation, error) {
	if depth <= 0 {
		depth = 1
	}

	visited := map[string]bool{entityID: true}
	frontier := []string{entityID}
	var entities []ports.GraphEntity
	var relations []ports.GraphRelation
	seenRels := map[string]bool{}

	for d := 0; d < depth && len(frontier) > 0; d++ {
		var nextFrontier []string
		for _, eid := range frontier {
			relIDs, err := s.client.SMembers(ctx, neighborsKey(eid)).Result()
			if err != nil {
				continue
			}
			for _, relID := range relIDs {
				if seenRels[relID] {
					continue
				}
				seenRels[relID] = true

				rel, err := s.loadRelation(ctx, relID)
				if err != nil {
					continue
				}
				relations = append(relations, *rel)

				// Determine the other end of the relation.
				otherID := rel.ToID
				if otherID == eid {
					otherID = rel.FromID
				}
				if !visited[otherID] {
					visited[otherID] = true
					nextFrontier = append(nextFrontier, otherID)

					ent, err := s.GetEntity(ctx, otherID)
					if err == nil {
						entities = append(entities, *ent)
					}
				}
			}
		}
		frontier = nextFrontier
	}

	return entities, relations, nil
}

// Search finds entities belonging to userID whose name contains the given text.
func (s *Store) Search(ctx context.Context, userID, text string, limit int) ([]ports.GraphEntity, error) {
	if limit <= 0 {
		limit = 10
	}

	entityIDs, err := s.client.SMembers(ctx, userEntitiesKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("falkordb: Search: %w", err)
	}

	lowerText := strings.ToLower(text)
	var results []ports.GraphEntity
	for _, eid := range entityIDs {
		if len(results) >= limit {
			break
		}
		ent, err := s.GetEntity(ctx, eid)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(ent.Name), lowerText) {
			results = append(results, *ent)
		}
	}
	return results, nil
}

// DeleteEntity removes an entity from both FalkorDB and the Redis mirror.
func (s *Store) DeleteEntity(ctx context.Context, id string) error {
	// Remove from graph.
	cypher := fmt.Sprintf("MATCH (n {id: '%s'}) DETACH DELETE n", escCypher(id))
	if err := s.graphQuery(ctx, cypher); err != nil {
		return fmt.Errorf("falkordb: DeleteEntity graph query: %w", err)
	}

	// Clean up relations from neighbor index.
	relIDs, _ := s.client.SMembers(ctx, neighborsKey(id)).Result()
	for _, relID := range relIDs {
		rel, err := s.loadRelation(ctx, relID)
		if err != nil {
			continue
		}
		// Remove this relation from the other endpoint's neighbor set.
		otherID := rel.ToID
		if otherID == id {
			otherID = rel.FromID
		}
		s.client.SRem(ctx, neighborsKey(otherID), relID)
		s.client.Del(ctx, relationKey(relID))
	}

	// Remove entity mirror and indexes.
	ent, _ := s.GetEntity(ctx, id)
	if ent != nil && ent.UserID != "" {
		s.client.SRem(ctx, userEntitiesKey(ent.UserID), id)
	}
	s.client.Del(ctx, entityKey(id), neighborsKey(id))

	return nil
}

// Query executes a raw Cypher query against FalkorDB and returns the result.
// Params are not directly supported by GRAPH.QUERY (no prepared statements);
// callers should interpolate safely or use this for read-only exploration.
func (s *Store) Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	// FalkorDB supports GRAPH.QUERY <key> <cypher> but does not support
	// parameterised queries in the Redis protocol layer. For safety we
	// only execute the raw cypher string here.
	result, err := s.client.Do(ctx, "GRAPH.QUERY", s.graphName, cypher).Result()
	if err != nil {
		return nil, fmt.Errorf("falkordb: Query: %w", err)
	}

	return parseGraphQueryResult(result), nil
}

// UserGraph returns all entities and relations belonging to a user.
func (s *Store) UserGraph(ctx context.Context, userID string) ([]ports.GraphEntity, []ports.GraphRelation, error) {
	entityIDs, err := s.client.SMembers(ctx, userEntitiesKey(userID)).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("falkordb: UserGraph: %w", err)
	}

	entitySet := map[string]bool{}
	var entities []ports.GraphEntity
	for _, eid := range entityIDs {
		entitySet[eid] = true
		ent, err := s.GetEntity(ctx, eid)
		if err != nil {
			continue
		}
		entities = append(entities, *ent)
	}

	// Collect relations between entities in this user's graph.
	seenRels := map[string]bool{}
	var relations []ports.GraphRelation
	for _, eid := range entityIDs {
		relIDs, _ := s.client.SMembers(ctx, neighborsKey(eid)).Result()
		for _, relID := range relIDs {
			if seenRels[relID] {
				continue
			}
			seenRels[relID] = true
			rel, err := s.loadRelation(ctx, relID)
			if err != nil {
				continue
			}
			// Only include relations where both endpoints belong to this user's graph.
			if entitySet[rel.FromID] && entitySet[rel.ToID] {
				relations = append(relations, *rel)
			}
		}
	}

	return entities, relations, nil
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// graphQuery sends a fire-and-forget GRAPH.QUERY command (for writes).
func (s *Store) graphQuery(ctx context.Context, cypher string) error {
	_, err := s.client.Do(ctx, "GRAPH.QUERY", s.graphName, cypher).Result()
	return err
}

// loadRelation reads a relation from the Redis mirror.
func (s *Store) loadRelation(ctx context.Context, relID string) (*ports.GraphRelation, error) {
	vals, err := s.client.HGetAll(ctx, relationKey(relID)).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("relation %q not found", relID)
	}
	return hashToRelation(vals), nil
}

// hashToEntity converts a Redis hash map to a GraphEntity.
func hashToEntity(vals map[string]string) *ports.GraphEntity {
	var props map[string]any
	if pj, ok := vals["properties_json"]; ok && pj != "" {
		json.Unmarshal([]byte(pj), &props)
	}
	createdAt, _ := time.Parse(time.RFC3339, vals["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339, vals["updated_at"])

	return &ports.GraphEntity{
		ID:         vals["id"],
		Type:       vals["type"],
		Name:       vals["name"],
		UserID:     vals["user_id"],
		Properties: props,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
}

// hashToRelation converts a Redis hash map to a GraphRelation.
func hashToRelation(vals map[string]string) *ports.GraphRelation {
	var meta map[string]any
	if mj, ok := vals["metadata_json"]; ok && mj != "" {
		json.Unmarshal([]byte(mj), &meta)
	}
	var weight float64
	if w, ok := vals["weight"]; ok {
		fmt.Sscanf(w, "%f", &weight)
	}

	return &ports.GraphRelation{
		ID:       vals["id"],
		FromID:   vals["from_id"],
		ToID:     vals["to_id"],
		Type:     vals["type"],
		Weight:   weight,
		Metadata: meta,
	}
}

// sanitizeLabel returns a safe Cypher node/relationship label. Defaults to
// "Entity" when the input is empty or entirely non-alphanumeric.
func sanitizeLabel(label string) string {
	var b strings.Builder
	for _, r := range label {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Entity"
	}
	result := b.String()
	// Cypher labels must start with a letter.
	if result[0] >= '0' && result[0] <= '9' {
		return "E_" + result
	}
	return result
}

// escCypher escapes single quotes in a string for Cypher string literals.
func escCypher(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// parseGraphQueryResult does a best-effort parse of FalkorDB's GRAPH.QUERY
// RESP response into []map[string]any. FalkorDB returns a nested array:
//
//	[header_row, [data_rows...], metadata_strings]
//
// Each header is a [column_type, column_name] pair. Each data row is an array
// of values corresponding to the headers. We extract column names and map
// each row's values.
func parseGraphQueryResult(raw interface{}) []map[string]any {
	arr, ok := raw.([]interface{})
	if !ok || len(arr) < 2 {
		return nil
	}

	// Extract column headers.
	headers, ok := arr[0].([]interface{})
	if !ok {
		return nil
	}
	colNames := make([]string, 0, len(headers))
	for _, h := range headers {
		switch hv := h.(type) {
		case []interface{}:
			// [type_int, "column_name"]
			if len(hv) >= 2 {
				if name, ok := hv[1].(string); ok {
					colNames = append(colNames, name)
				}
			}
		case string:
			colNames = append(colNames, hv)
		}
	}

	// Extract data rows.
	dataRows, ok := arr[1].([]interface{})
	if !ok {
		return nil
	}

	var results []map[string]any
	for _, row := range dataRows {
		rowArr, ok := row.([]interface{})
		if !ok {
			continue
		}
		m := make(map[string]any, len(colNames))
		for i, val := range rowArr {
			if i < len(colNames) {
				m[colNames[i]] = val
			}
		}
		results = append(results, m)
	}

	return results
}

// Compile-time interface check.
var _ ports.GraphBackend = (*Store)(nil)

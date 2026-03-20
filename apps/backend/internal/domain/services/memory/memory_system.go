package memory

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BangRocket/MyPal/apps/backend/internal/domain/ports"
	"github.com/google/uuid"
)

// Memory represents a unified memory result from either vector or graph sources.
type Memory struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Score    float64        `json:"score"`
	Source   string         `json:"source"` // "vector" or "graph"
	Metadata map[string]any `json:"metadata"`
}

// UserProfile contains bootstrap information for initialising a user's memory graph.
type UserProfile struct {
	Name          string
	Preferences   map[string]string
	Interests     []string
	Projects      []string
	Relationships []struct{ Name, Relation string }
}

// MemorySystem provides a unified API over vector and graph memory backends.
// The graph backend is optional — when nil, only vector operations are used.
type MemorySystem struct {
	Vector *VectorMemory
	Graph  ports.GraphBackend
}

// NewMemorySystem creates a MemorySystem. graph may be nil.
func NewMemorySystem(vector *VectorMemory, graph ports.GraphBackend) *MemorySystem {
	return &MemorySystem{
		Vector: vector,
		Graph:  graph,
	}
}

// Remember stores content in vector memory and, when a graph backend is
// available, creates a "memory" entity linked to the user entity.
func (m *MemorySystem) Remember(ctx context.Context, userID, content string, metadata map[string]any) error {
	// Always store in vector memory.
	if err := m.Vector.Remember(ctx, userID, content, metadata); err != nil {
		return fmt.Errorf("memory system: vector remember: %w", err)
	}

	if m.Graph == nil {
		return nil
	}

	// Ensure the user entity exists (idempotent: ignore error if already present).
	userEntityID := "user:" + userID
	if _, err := m.Graph.GetEntity(ctx, userEntityID); err != nil {
		_ = m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:        userEntityID,
			Type:      "user",
			Name:      userID,
			UserID:    userID,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		})
	}

	// Store the content as a memory entity.
	memEntityID := uuid.New().String()
	if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
		ID:     memEntityID,
		Type:   "memory",
		Name:   truncate(content, 100),
		UserID: userID,
		Properties: map[string]any{
			"content": content,
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("memory system: graph add memory entity: %w", err)
	}

	// Link memory to user.
	if err := m.Graph.AddRelation(ctx, ports.GraphRelation{
		ID:     uuid.New().String(),
		FromID: userEntityID,
		ToID:   memEntityID,
		Type:   "HAS_MEMORY",
		Weight: 1.0,
	}); err != nil {
		return fmt.Errorf("memory system: graph add memory relation: %w", err)
	}

	// Simple entity extraction: find quoted terms and capitalized name patterns.
	entities := extractSimpleEntities(content)
	for _, name := range entities {
		entID := uuid.New().String()
		if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:        entID,
			Type:      "entity",
			Name:      name,
			UserID:    userID,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			continue // best effort
		}
		_ = m.Graph.AddRelation(ctx, ports.GraphRelation{
			ID:     uuid.New().String(),
			FromID: memEntityID,
			ToID:   entID,
			Type:   "MENTIONS",
			Weight: 1.0,
		})
	}

	return nil
}

// Recall searches vector memory and optionally graph memory, merges and
// deduplicates results, and returns them sorted by score descending.
func (m *MemorySystem) Recall(ctx context.Context, userID, query string, topK int) ([]Memory, error) {
	if topK <= 0 {
		topK = 10
	}

	// Vector search.
	vectorResults, err := m.Vector.Recall(ctx, userID, query, topK)
	if err != nil {
		return nil, fmt.Errorf("memory system: vector recall: %w", err)
	}

	var memories []Memory
	for _, vr := range vectorResults {
		memories = append(memories, Memory{
			ID:       vr.ID,
			Content:  vr.Content,
			Score:    vr.Score,
			Source:   "vector",
			Metadata: vr.Metadata,
		})
	}

	// Graph search (optional).
	if m.Graph != nil {
		graphEntities, err := m.Graph.Search(ctx, userID, query, topK)
		if err == nil {
			for _, ge := range graphEntities {
				content := ge.Name
				if c, ok := ge.Properties["content"].(string); ok && c != "" {
					content = c
				}
				memories = append(memories, Memory{
					ID:      ge.ID,
					Content: content,
					Score:   0.5, // graph results get a default score
					Source:  "graph",
					Metadata: map[string]any{
						"type":       ge.Type,
						"properties": ge.Properties,
					},
				})
			}
		}
	}

	// Deduplicate by content similarity (exact substring match).
	memories = deduplicateMemories(memories)

	// Sort by score descending.
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Score > memories[j].Score
	})

	// Limit to topK.
	if len(memories) > topK {
		memories = memories[:topK]
	}

	return memories, nil
}

// Bootstrap initialises a user's memory graph with profile information.
// Each piece of profile data is stored as both a graph entity and a vector
// memory entry for semantic search.
func (m *MemorySystem) Bootstrap(ctx context.Context, userID string, profile UserProfile) error {
	if m.Graph == nil {
		return nil
	}

	// Create user entity.
	userEntityID := "user:" + userID
	userName := profile.Name
	if userName == "" {
		userName = userID
	}
	if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
		ID:        userEntityID,
		Type:      "user",
		Name:      userName,
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("memory system: bootstrap user entity: %w", err)
	}

	// Add preferences.
	for key, value := range profile.Preferences {
		prefID := uuid.New().String()
		content := fmt.Sprintf("Preference: %s = %s", key, value)
		if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:     prefID,
			Type:   "preference",
			Name:   key,
			UserID: userID,
			Properties: map[string]any{
				"key":   key,
				"value": value,
			},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("memory system: bootstrap preference %q: %w", key, err)
		}
		_ = m.Graph.AddRelation(ctx, ports.GraphRelation{
			ID:     uuid.New().String(),
			FromID: userEntityID,
			ToID:   prefID,
			Type:   "HAS_PREFERENCE",
			Weight: 1.0,
		})
		// Also store in vector memory for semantic search.
		_ = m.Vector.Remember(ctx, userID, content, map[string]any{
			"type":   "preference",
			"key":    key,
			"source": "bootstrap",
		})
	}

	// Add interests.
	for _, interest := range profile.Interests {
		intID := uuid.New().String()
		content := fmt.Sprintf("Interest: %s", interest)
		if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:        intID,
			Type:      "interest",
			Name:      interest,
			UserID:    userID,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("memory system: bootstrap interest %q: %w", interest, err)
		}
		_ = m.Graph.AddRelation(ctx, ports.GraphRelation{
			ID:     uuid.New().String(),
			FromID: userEntityID,
			ToID:   intID,
			Type:   "HAS_INTEREST",
			Weight: 1.0,
		})
		_ = m.Vector.Remember(ctx, userID, content, map[string]any{
			"type":   "interest",
			"source": "bootstrap",
		})
	}

	// Add projects.
	for _, project := range profile.Projects {
		projID := uuid.New().String()
		content := fmt.Sprintf("Project: %s", project)
		if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:        projID,
			Type:      "project",
			Name:      project,
			UserID:    userID,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("memory system: bootstrap project %q: %w", project, err)
		}
		_ = m.Graph.AddRelation(ctx, ports.GraphRelation{
			ID:     uuid.New().String(),
			FromID: userEntityID,
			ToID:   projID,
			Type:   "WORKS_ON",
			Weight: 1.0,
		})
		_ = m.Vector.Remember(ctx, userID, content, map[string]any{
			"type":   "project",
			"source": "bootstrap",
		})
	}

	// Add relationships.
	for _, rel := range profile.Relationships {
		relEntityID := uuid.New().String()
		content := fmt.Sprintf("Relationship: %s is %s", rel.Name, rel.Relation)
		if err := m.Graph.AddEntity(ctx, ports.GraphEntity{
			ID:     relEntityID,
			Type:   "person",
			Name:   rel.Name,
			UserID: userID,
			Properties: map[string]any{
				"relation": rel.Relation,
			},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("memory system: bootstrap relationship %q: %w", rel.Name, err)
		}
		_ = m.Graph.AddRelation(ctx, ports.GraphRelation{
			ID:     uuid.New().String(),
			FromID: userEntityID,
			ToID:   relEntityID,
			Type:   sanitizeRelationType(rel.Relation),
			Weight: 1.0,
		})
		_ = m.Vector.Remember(ctx, userID, content, map[string]any{
			"type":     "relationship",
			"name":     rel.Name,
			"relation": rel.Relation,
			"source":   "bootstrap",
		})
	}

	return nil
}

// Forget removes all vector memories and graph entities for the given user.
func (m *MemorySystem) Forget(ctx context.Context, userID string) error {
	// Delete all vector memories for user.
	if err := m.Vector.ForgetAll(ctx, userID); err != nil {
		return fmt.Errorf("memory system: forget vector memories: %w", err)
	}

	// Delete graph entities for user.
	if m.Graph != nil {
		entities, _, err := m.Graph.UserGraph(ctx, userID)
		if err != nil {
			return fmt.Errorf("memory system: forget get user graph: %w", err)
		}
		for _, ent := range entities {
			if err := m.Graph.DeleteEntity(ctx, ent.ID); err != nil {
				return fmt.Errorf("memory system: forget delete entity %s: %w", ent.ID, err)
			}
		}
	}

	return nil
}

// extractSimpleEntities uses simple heuristics to find entity names in text:
// - Quoted strings ("like this" or 'like this')
// - Capitalized multi-word sequences (e.g. "John Smith", "New York")
func extractSimpleEntities(text string) []string {
	seen := make(map[string]bool)
	var entities []string

	// Quoted strings.
	quotedRe := regexp.MustCompile(`["']([^"']{2,50})["']`)
	for _, match := range quotedRe.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(match[1])
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			entities = append(entities, name)
		}
	}

	// Capitalized name patterns (2-4 consecutive capitalized words).
	nameRe := regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,3})\b`)
	for _, match := range nameRe.FindAllStringSubmatch(text, -1) {
		name := match[1]
		lower := strings.ToLower(name)
		if !seen[lower] {
			seen[lower] = true
			entities = append(entities, name)
		}
	}

	return entities
}

// deduplicateMemories removes duplicate memories where one content is a
// substring of another, keeping the higher-scored entry.
func deduplicateMemories(memories []Memory) []Memory {
	if len(memories) <= 1 {
		return memories
	}

	// Sort by score descending so higher-scored entries are kept.
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Score > memories[j].Score
	})

	var result []Memory
	for _, mem := range memories {
		isDup := false
		contentLower := strings.ToLower(mem.Content)
		for _, existing := range result {
			existingLower := strings.ToLower(existing.Content)
			if contentLower == existingLower ||
				strings.Contains(existingLower, contentLower) ||
				strings.Contains(contentLower, existingLower) {
				isDup = true
				break
			}
		}
		if !isDup {
			result = append(result, mem)
		}
	}

	return result
}

// truncate returns the first n characters of s, appending "..." if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// sanitizeRelationType converts a human-readable relation string to a
// graph-safe uppercase relation type (e.g. "best friend" -> "BEST_FRIEND").
func sanitizeRelationType(rel string) string {
	upper := strings.ToUpper(rel)
	// Replace non-alphanumeric with underscore.
	var b strings.Builder
	for _, r := range upper {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := strings.TrimRight(b.String(), "_")
	if result == "" {
		return "KNOWS"
	}
	return result
}

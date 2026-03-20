package migrate_memory

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

// claraExportConfig holds settings for a clara-export migration run.
type claraExportConfig struct {
	ArchivePath string

	// Vector target (Qdrant)
	QdrantEndpoint   string
	QdrantCollection string
	QdrantAPIKey     string

	// Vector target (pgvector) — used when QdrantEndpoint is empty.
	TargetDSN string

	// Graph target (FalkorDB)
	FalkorDBAddr     string
	FalkorDBPassword string
	FalkorDBGraph    string

	BatchSize int
	DryRun    bool
	UserID    string // optional: only import records for this user
}

// manifest represents the manifest.json in a clara export archive.
type manifest struct {
	Version           string         `json:"version"`
	CreatedAt         string         `json:"created_at"`
	SourceBackends    map[string]any `json:"source_backends"`
	Filters           map[string]any `json:"filters"`
	EmbeddingModel    string         `json:"embedding_model"`
	EmbeddingDims     int            `json:"embedding_dimensions"`
	RecordCounts      map[string]int `json:"record_counts"`
}

// vectorRecord represents a line in vectors/memories.jsonl.
type vectorRecord struct {
	ID      any            `json:"id"`
	Vector  []float64      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// graphNodeRecord represents a line in graph/nodes.jsonl.
type graphNodeRecord struct {
	ID         any            `json:"id"`
	Properties map[string]any `json:"properties"`
}

// graphEdgeRecord represents a line in graph/edges.jsonl.
type graphEdgeRecord struct {
	Source     string         `json:"source"`
	Relation   string         `json:"relation"`
	Properties map[string]any `json:"properties"`
	Target     string         `json:"target"`
}

// runClaraExportMigration reads a mypalclara export archive and writes
// vectors to Qdrant (or pgvector) and graph data to FalkorDB.
func runClaraExportMigration(ctx context.Context, cfg claraExportConfig) error {
	// Extract archive into memory.
	files, err := extractArchive(cfg.ArchivePath)
	if err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	// Parse and validate manifest.
	manifestData, ok := files["manifest.json"]
	if !ok {
		return fmt.Errorf("archive missing manifest.json")
	}
	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != "1" {
		return fmt.Errorf("unsupported manifest version %q (expected \"1\")", m.Version)
	}
	fmt.Printf("Archive: version=%s created=%s embedding=%s dims=%d\n",
		m.Version, m.CreatedAt, m.EmbeddingModel, m.EmbeddingDims)
	for k, v := range m.RecordCounts {
		fmt.Printf("  %s: %d records\n", k, v)
	}

	if cfg.DryRun {
		fmt.Println("\nDry-run mode: no writes performed.")
		return nil
	}

	var errs []error

	// Import vectors.
	if vectorData, ok := files["vectors/memories.jsonl"]; ok {
		fmt.Println("\n=== Vector import ===")
		if cfg.QdrantEndpoint != "" {
			n, err := importVectorsQdrant(ctx, vectorData, cfg)
			if err != nil {
				errs = append(errs, fmt.Errorf("vector import: %w", err))
			}
			fmt.Printf("Imported %d vectors into Qdrant (%s/%s)\n", n, cfg.QdrantEndpoint, cfg.QdrantCollection)
		} else if cfg.TargetDSN != "" {
			n, importErrs := importVectorsPgvector(ctx, vectorData, cfg)
			if len(importErrs) > 0 {
				for _, e := range importErrs {
					errs = append(errs, e)
				}
			}
			fmt.Printf("Imported %d vectors into pgvector\n", n)
		} else {
			fmt.Println("Skipping vectors: no --qdrant-endpoint or --target-dsn specified")
		}
	} else {
		fmt.Println("No vectors/memories.jsonl in archive")
	}

	// Import graph nodes and edges.
	nodesData := files["graph/nodes.jsonl"]
	edgesData := files["graph/edges.jsonl"]
	if (len(nodesData) > 0 || len(edgesData) > 0) && cfg.FalkorDBAddr != "" {
		fmt.Println("\n=== Graph import ===")
		nNodes, nEdges, graphErrs := importGraphFalkorDB(ctx, nodesData, edgesData, cfg)
		if len(graphErrs) > 0 {
			for _, e := range graphErrs {
				errs = append(errs, e)
			}
		}
		fmt.Printf("Imported %d nodes and %d edges into FalkorDB (%s/%s)\n",
			nNodes, nEdges, cfg.FalkorDBAddr, cfg.FalkorDBGraph)
	} else if len(nodesData) > 0 || len(edgesData) > 0 {
		fmt.Println("Skipping graph: no --falkordb-addr specified")
	} else {
		fmt.Println("No graph data in archive")
	}

	if len(errs) > 0 {
		fmt.Printf("\nCompleted with %d errors:\n", len(errs))
		for _, e := range errs {
			fmt.Printf("  - %v\n", e)
		}
		return fmt.Errorf("migration completed with %d errors", len(errs))
	}
	fmt.Println("\nMigration completed successfully.")
	return nil
}

// extractArchive reads a .tar.gz file and returns a map of path -> contents.
func extractArchive(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	files := make(map[string][]byte)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		files[hdr.Name] = data
	}
	return files, nil
}

// ---------------------------------------------------------------------------
// Vector import: Qdrant
// ---------------------------------------------------------------------------

func importVectorsQdrant(ctx context.Context, data []byte, cfg claraExportConfig) (int, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	endpoint := cfg.QdrantEndpoint
	collection := cfg.QdrantCollection

	// Ensure collection exists — detect dimensions from first record.
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB line limit
	dims := 0
	if scanner.Scan() {
		var rec vectorRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err == nil {
			dims = len(rec.Vector)
		}
	}
	if dims > 0 {
		initBody, _ := json.Marshal(map[string]any{
			"vectors": map[string]any{"size": dims, "distance": "Cosine"},
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
			fmt.Sprintf("%s/collections/%s", endpoint, collection),
			bytes.NewReader(initBody))
		req.Header.Set("Content-Type", "application/json")
		if cfg.QdrantAPIKey != "" {
			req.Header.Set("api-key", cfg.QdrantAPIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("init collection: %w", err)
		}
		resp.Body.Close()
		// 200=created, 409=exists — both fine.
	}

	// Upsert in batches.
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	count := 0
	var batch []map[string]any

	// Re-scan from the beginning.
	scanner = bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		body, _ := json.Marshal(map[string]any{"points": batch})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
			fmt.Sprintf("%s/collections/%s/points", endpoint, collection),
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cfg.QdrantAPIKey != "" {
			req.Header.Set("api-key", cfg.QdrantAPIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("qdrant upsert: status %d: %s", resp.StatusCode, string(respBody))
		}
		batch = batch[:0]
		return nil
	}

	for scanner.Scan() {
		var rec vectorRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}

		// Filter by user if requested.
		if cfg.UserID != "" {
			if uid, _ := rec.Payload["user_id"].(string); uid != cfg.UserID {
				continue
			}
		}

		// Map payload to MyPal's format.
		pointID := fmt.Sprintf("%v", rec.ID)
		userID, _ := rec.Payload["user_id"].(string)
		content, _ := rec.Payload["memory"].(string)
		if content == "" {
			content, _ = rec.Payload["data"].(string)
		}

		payload := map[string]any{
			"content":    content,
			"user_id":    userID,
			"metadata":   rec.Payload,
			"created_at": time.Now().UTC().Format(time.RFC3339),
		}
		// Preserve original created_at if present.
		if ca, ok := rec.Payload["created_at"]; ok {
			payload["created_at"] = ca
		}
		payload["metadata"].(map[string]any)["migrated_from"] = "mypalclara"

		batch = append(batch, map[string]any{
			"id":      pointID,
			"vector":  rec.Vector,
			"payload": payload,
		})
		count++

		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return count, err
			}
			fmt.Printf("  vectors: %d upserted...\n", count)
		}
	}

	if err := flush(); err != nil {
		return count, err
	}

	return count, nil
}

// ---------------------------------------------------------------------------
// Vector import: pgvector
// ---------------------------------------------------------------------------

func importVectorsPgvector(ctx context.Context, data []byte, cfg claraExportConfig) (int, []error) {
	// Reuse the existing mem0 pgvector upsert logic.
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var rows []mem0Row
	for scanner.Scan() {
		var rec vectorRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}

		if cfg.UserID != "" {
			if uid, _ := rec.Payload["user_id"].(string); uid != cfg.UserID {
				continue
			}
		}

		pointID := fmt.Sprintf("%v", rec.ID)
		userID, _ := rec.Payload["user_id"].(string)
		content, _ := rec.Payload["memory"].(string)
		if content == "" {
			content, _ = rec.Payload["data"].(string)
		}

		// Add provenance marker.
		rec.Payload["migrated_from"] = "mypalclara"
		metaJSON, _ := json.Marshal(rec.Payload)

		// Format embedding as pgvector text: "[0.1,0.2,...]"
		vecParts := make([]string, len(rec.Vector))
		for i, v := range rec.Vector {
			vecParts[i] = fmt.Sprintf("%g", v)
		}
		embedding := "[" + strings.Join(vecParts, ",") + "]"

		rows = append(rows, mem0Row{
			ID:        pointID,
			UserID:    userID,
			Content:   content,
			Metadata:  metaJSON,
			Embedding: embedding,
			CreatedAt: time.Now(),
		})
	}

	fmt.Printf("  Parsed %d vector records from archive\n", len(rows))

	if len(rows) == 0 {
		return 0, nil
	}

	// Open target DB and upsert.
	import_cfg := migrationConfig{
		TargetDSN: cfg.TargetDSN,
		BatchSize: cfg.BatchSize,
	}
	_ = import_cfg // appease vet

	db, err := openPgx(cfg.TargetDSN)
	if err != nil {
		return 0, []error{err}
	}
	defer db.Close()

	return upsertVectors(ctx, db, rows, cfg.BatchSize)
}

// ---------------------------------------------------------------------------
// Graph import: FalkorDB
// ---------------------------------------------------------------------------

func importGraphFalkorDB(ctx context.Context, nodesData, edgesData []byte, cfg claraExportConfig) (int, int, []error) {
	graphName := cfg.FalkorDBGraph
	if graphName == "" {
		graphName = "mypal_memory"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.FalkorDBAddr,
		Password: cfg.FalkorDBPassword,
	})
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return 0, 0, []error{fmt.Errorf("falkordb connect: %w", err)}
	}

	var errs []error

	// --- Import nodes ---
	nodeCount := 0
	// Track name->id for edge resolution (clara export uses names, not IDs, in edges).
	nameToID := make(map[string]string)

	if len(nodesData) > 0 {
		scanner := bufio.NewScanner(bytes.NewReader(nodesData))
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			var rec graphNodeRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				continue
			}

			props := rec.Properties
			if props == nil {
				props = make(map[string]any)
			}

			// Filter by user if requested.
			if cfg.UserID != "" {
				if uid, _ := props["user_id"].(string); uid != cfg.UserID {
					continue
				}
			}

			name, _ := props["name"].(string)
			userID, _ := props["user_id"].(string)
			entityType, _ := props["type"].(string)
			if entityType == "" {
				entityType = "Entity"
			}

			entityID := uuid.New().String()
			nameToID[name] = entityID

			now := time.Now().UTC().Format(time.RFC3339)
			createdAt := now
			if ca, ok := props["created_at"].(string); ok && ca != "" {
				createdAt = ca
			}

			// Cypher: create node.
			cypher := fmt.Sprintf(
				"CREATE (n:%s {id: '%s', name: '%s', user_id: '%s', created_at: '%s', updated_at: '%s'})",
				sanitizeLabel(entityType),
				escCypher(entityID),
				escCypher(name),
				escCypher(userID),
				createdAt,
				now,
			)
			if _, err := client.Do(ctx, "GRAPH.QUERY", graphName, cypher).Result(); err != nil {
				errs = append(errs, fmt.Errorf("node %q: %w", name, err))
				continue
			}

			// Mirror to Redis hash.
			propsJSON, _ := json.Marshal(props)
			client.HSet(ctx, "entity:"+entityID, map[string]interface{}{
				"id":              entityID,
				"type":            entityType,
				"name":            name,
				"user_id":         userID,
				"properties_json": string(propsJSON),
				"created_at":      createdAt,
				"updated_at":      now,
			})

			if userID != "" {
				client.SAdd(ctx, "user_entities:"+userID, entityID)
			}

			nodeCount++
			if nodeCount%100 == 0 {
				fmt.Printf("  nodes: %d imported...\n", nodeCount)
			}
		}
		fmt.Printf("  nodes: %d total\n", nodeCount)
	}

	// --- Import edges ---
	edgeCount := 0
	if len(edgesData) > 0 {
		scanner := bufio.NewScanner(bytes.NewReader(edgesData))
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

		for scanner.Scan() {
			var rec graphEdgeRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				continue
			}

			relType := rec.Relation
			if relType == "" {
				relType = "RELATED_TO"
			}

			// Resolve source/target names to entity IDs.
			fromID, fromOK := nameToID[rec.Source]
			toID, toOK := nameToID[rec.Target]

			if !fromOK || !toOK {
				// Nodes not in this import — create stub entities.
				if !fromOK {
					fromID = uuid.New().String()
					nameToID[rec.Source] = fromID
					stubNode(ctx, client, graphName, fromID, rec.Source)
				}
				if !toOK {
					toID = uuid.New().String()
					nameToID[rec.Target] = toID
					stubNode(ctx, client, graphName, toID, rec.Target)
				}
			}

			relID := uuid.New().String()
			weight := 0.0
			if w, ok := rec.Properties["weight"].(float64); ok {
				weight = w
			}

			// Cypher: create relationship.
			cypher := fmt.Sprintf(
				"MATCH (a {id: '%s'}), (b {id: '%s'}) CREATE (a)-[r:%s {id: '%s', weight: %f}]->(b)",
				escCypher(fromID),
				escCypher(toID),
				sanitizeLabel(relType),
				escCypher(relID),
				weight,
			)
			if _, err := client.Do(ctx, "GRAPH.QUERY", graphName, cypher).Result(); err != nil {
				errs = append(errs, fmt.Errorf("edge %s->%s: %w", rec.Source, rec.Target, err))
				continue
			}

			// Mirror to Redis.
			metaJSON, _ := json.Marshal(rec.Properties)
			client.HSet(ctx, "relation:"+relID, map[string]interface{}{
				"id":            relID,
				"from_id":       fromID,
				"to_id":         toID,
				"type":          relType,
				"weight":        fmt.Sprintf("%f", weight),
				"metadata_json": string(metaJSON),
			})
			client.SAdd(ctx, "neighbors:"+fromID, relID)
			client.SAdd(ctx, "neighbors:"+toID, relID)

			edgeCount++
			if edgeCount%100 == 0 {
				fmt.Printf("  edges: %d imported...\n", edgeCount)
			}
		}
		fmt.Printf("  edges: %d total\n", edgeCount)
	}

	return nodeCount, edgeCount, errs
}

// stubNode creates a minimal entity node for edge endpoints that weren't
// in the nodes export (e.g. when importing edges only).
func stubNode(ctx context.Context, client *redis.Client, graphName, id, name string) {
	now := time.Now().UTC().Format(time.RFC3339)
	cypher := fmt.Sprintf(
		"CREATE (n:Entity {id: '%s', name: '%s', user_id: '', created_at: '%s', updated_at: '%s'})",
		escCypher(id), escCypher(name), now, now,
	)
	client.Do(ctx, "GRAPH.QUERY", graphName, cypher)
	client.HSet(ctx, "entity:"+id, map[string]interface{}{
		"id":              id,
		"type":            "Entity",
		"name":            name,
		"user_id":         "",
		"properties_json": "{}",
		"created_at":      now,
		"updated_at":      now,
	})
}

// sanitizeLabel returns a safe Cypher node/relationship label.
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
	if result[0] >= '0' && result[0] <= '9' {
		return "E_" + result
	}
	return result
}

// escCypher escapes single quotes for Cypher string literals.
func escCypher(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// openPgx opens a pgx database/sql connection.
func openPgx(dsn string) (*sql.DB, error) {
	return sql.Open("pgx", dsn)
}

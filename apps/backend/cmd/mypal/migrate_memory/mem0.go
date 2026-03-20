package migrate_memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver
)

// migrationConfig holds all settings for a migration run.
type migrationConfig struct {
	SourceDSN string
	TargetDSN string
	BatchSize int
	DryRun    bool
}

// mem0Row represents a single row from mem0's memories table.
type mem0Row struct {
	ID        string
	UserID    string
	Content   string
	Metadata  json.RawMessage
	Embedding string // pgvector text format, e.g. "[0.1,0.2,...]"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// runMem0Migration connects to source and target databases, reads all mem0
// memories, and upserts them into MyPal's memory_vectors table.
func runMem0Migration(ctx context.Context, cfg migrationConfig) error {
	// --- Connect to source (mem0) ---
	srcDB, err := sql.Open("pgx", cfg.SourceDSN)
	if err != nil {
		return fmt.Errorf("connect to source: %w", err)
	}
	defer srcDB.Close()

	if err := srcDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping source: %w", err)
	}
	fmt.Println("Connected to source (mem0) database.")

	// --- Read source rows ---
	rows, err := readMem0Vectors(ctx, srcDB)
	if err != nil {
		return fmt.Errorf("read source vectors: %w", err)
	}
	fmt.Printf("Found %d vector memories in source.\n", len(rows))

	// --- Read graph data (entities + relations) if present ---
	entities, relations, err := readMem0Graph(ctx, srcDB)
	if err != nil {
		// Graph tables may not exist; treat as non-fatal.
		fmt.Printf("Graph data not available (skipping): %v\n", err)
		entities, relations = nil, nil
	} else {
		fmt.Printf("Found %d graph entities and %d graph relations in source.\n", len(entities), len(relations))
	}

	if cfg.DryRun {
		fmt.Println("Dry-run mode: no writes performed.")
		return nil
	}

	// --- Connect to target (MyPal) ---
	tgtDB, err := sql.Open("pgx", cfg.TargetDSN)
	if err != nil {
		return fmt.Errorf("connect to target: %w", err)
	}
	defer tgtDB.Close()

	if err := tgtDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping target: %w", err)
	}
	fmt.Println("Connected to target (MyPal) database.")

	// --- Upsert vectors ---
	migrated, errs := upsertVectors(ctx, tgtDB, rows, cfg.BatchSize)
	fmt.Printf("Migrated %d/%d vector memories.\n", migrated, len(rows))
	if len(errs) > 0 {
		fmt.Printf("Encountered %d errors during vector migration:\n", len(errs))
		for _, e := range errs {
			fmt.Printf("  - %v\n", e)
		}
	}

	// --- Upsert graph data ---
	if len(entities) > 0 || len(relations) > 0 {
		ge, gr, graphErrs := upsertGraph(ctx, tgtDB, entities, relations)
		fmt.Printf("Migrated %d entities and %d relations.\n", ge, gr)
		if len(graphErrs) > 0 {
			fmt.Printf("Encountered %d errors during graph migration:\n", len(graphErrs))
			for _, e := range graphErrs {
				fmt.Printf("  - %v\n", e)
			}
			errs = append(errs, graphErrs...)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("migration completed with %d errors", len(errs))
	}
	fmt.Println("Migration completed successfully.")
	return nil
}

// readMem0Vectors reads all rows from mem0's memories table.
func readMem0Vectors(ctx context.Context, db *sql.DB) ([]mem0Row, error) {
	query := `SELECT id, user_id, content, metadata, embedding::text, created_at, updated_at
		FROM memories ORDER BY created_at`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var result []mem0Row
	for rows.Next() {
		var r mem0Row
		if err := rows.Scan(&r.ID, &r.UserID, &r.Content, &r.Metadata, &r.Embedding, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// mem0GraphEntity represents a row from mem0's graph entities table (if present).
type mem0GraphEntity struct {
	ID         string
	Type       string
	Name       string
	UserID     string
	Properties json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// mem0GraphRelation represents a row from mem0's graph relations table (if present).
type mem0GraphRelation struct {
	ID       string
	FromID   string
	ToID     string
	Type     string
	Weight   float64
	Metadata json.RawMessage
}

// readMem0Graph attempts to read entities and relations from mem0's graph tables.
// Returns empty slices (not an error) if the tables don't exist.
func readMem0Graph(ctx context.Context, db *sql.DB) ([]mem0GraphEntity, []mem0GraphRelation, error) {
	// Check if the graph_entities table exists.
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'graph_entities')`).Scan(&exists)
	if err != nil {
		return nil, nil, fmt.Errorf("check graph_entities table: %w", err)
	}
	if !exists {
		return nil, nil, fmt.Errorf("graph_entities table not found")
	}

	// Read entities.
	eRows, err := db.QueryContext(ctx,
		`SELECT id, type, name, user_id, properties, created_at, updated_at FROM graph_entities ORDER BY created_at`)
	if err != nil {
		return nil, nil, fmt.Errorf("query graph_entities: %w", err)
	}
	defer eRows.Close()

	var entities []mem0GraphEntity
	for eRows.Next() {
		var e mem0GraphEntity
		if err := eRows.Scan(&e.ID, &e.Type, &e.Name, &e.UserID, &e.Properties, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan graph entity: %w", err)
		}
		entities = append(entities, e)
	}
	if err := eRows.Err(); err != nil {
		return nil, nil, err
	}

	// Read relations (if table exists).
	err = db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'graph_relations')`).Scan(&exists)
	if err != nil {
		return entities, nil, fmt.Errorf("check graph_relations table: %w", err)
	}
	if !exists {
		return entities, nil, nil
	}

	rRows, err := db.QueryContext(ctx,
		`SELECT id, from_id, to_id, type, weight, metadata FROM graph_relations`)
	if err != nil {
		return entities, nil, fmt.Errorf("query graph_relations: %w", err)
	}
	defer rRows.Close()

	var relations []mem0GraphRelation
	for rRows.Next() {
		var r mem0GraphRelation
		if err := rRows.Scan(&r.ID, &r.FromID, &r.ToID, &r.Type, &r.Weight, &r.Metadata); err != nil {
			return entities, nil, fmt.Errorf("scan graph relation: %w", err)
		}
		relations = append(relations, r)
	}
	return entities, relations, rRows.Err()
}

// upsertVectors inserts mem0 rows into MyPal's memory_vectors table in batches.
// It returns the count of successfully migrated rows and any errors encountered.
func upsertVectors(ctx context.Context, db *sql.DB, rows []mem0Row, batchSize int) (int, []error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	var (
		migrated int
		errs     []error
	)

	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[i:end]

		n, err := upsertVectorBatch(ctx, db, batch)
		migrated += n
		if err != nil {
			errs = append(errs, fmt.Errorf("batch %d-%d: %w", i, end-1, err))
		}
	}
	return migrated, errs
}

// upsertVectorBatch upserts a single batch of rows into memory_vectors.
func upsertVectorBatch(ctx context.Context, db *sql.DB, batch []mem0Row) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}

	// Build a multi-row INSERT ... ON CONFLICT upsert.
	var (
		sb     strings.Builder
		params []any
		idx    int
	)

	sb.WriteString(`INSERT INTO memory_vectors (id, user_id, content, metadata, embedding, created_at)
		VALUES `)

	for i, r := range batch {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d)",
			idx+1, idx+2, idx+3, idx+4, idx+5, idx+6))

		// Map metadata: add migrated_from key to track provenance.
		meta := mapMetadata(r.Metadata)

		params = append(params, r.ID, r.UserID, r.Content, meta, r.Embedding, r.CreatedAt)
		idx += 6
	}

	sb.WriteString(` ON CONFLICT (id) DO UPDATE SET
		content    = EXCLUDED.content,
		metadata   = EXCLUDED.metadata,
		embedding  = EXCLUDED.embedding,
		created_at = EXCLUDED.created_at`)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}

	_, err = tx.ExecContext(ctx, sb.String(), params...)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("exec upsert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(batch), nil
}

// mapMetadata enriches the existing mem0 metadata with a migration provenance
// marker. Returns a JSON string suitable for JSONB insertion.
func mapMetadata(raw json.RawMessage) string {
	m := make(map[string]any)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	m["migrated_from"] = "mem0"
	out, _ := json.Marshal(m)
	return string(out)
}

// upsertGraph inserts graph entities and relations into MyPal's graph tables.
// These match the filegraph/falkordb schema used by ports.GraphBackend.
func upsertGraph(ctx context.Context, db *sql.DB, entities []mem0GraphEntity, relations []mem0GraphRelation) (int, int, []error) {
	var errs []error

	// Ensure target graph tables exist.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS graph_entities (
			id         TEXT PRIMARY KEY,
			type       TEXT NOT NULL DEFAULT '',
			name       TEXT NOT NULL DEFAULT '',
			user_id    TEXT NOT NULL DEFAULT '',
			properties JSONB,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS graph_relations (
			id       TEXT PRIMARY KEY,
			from_id  TEXT NOT NULL,
			to_id    TEXT NOT NULL,
			type     TEXT NOT NULL DEFAULT '',
			weight   DOUBLE PRECISION DEFAULT 0,
			metadata JSONB
		)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return 0, 0, []error{fmt.Errorf("create graph table: %w", err)}
		}
	}

	// Upsert entities.
	eCount := 0
	for _, e := range entities {
		props := "{}"
		if len(e.Properties) > 0 {
			props = string(e.Properties)
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO graph_entities (id, type, name, user_id, properties, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type, name = EXCLUDED.name, user_id = EXCLUDED.user_id,
				properties = EXCLUDED.properties, updated_at = EXCLUDED.updated_at`,
			e.ID, e.Type, e.Name, e.UserID, props, e.CreatedAt, e.UpdatedAt)
		if err != nil {
			errs = append(errs, fmt.Errorf("upsert entity %s: %w", e.ID, err))
			continue
		}
		eCount++
	}

	// Upsert relations.
	rCount := 0
	for _, r := range relations {
		meta := "{}"
		if len(r.Metadata) > 0 {
			meta = string(r.Metadata)
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO graph_relations (id, from_id, to_id, type, weight, metadata)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET
				from_id = EXCLUDED.from_id, to_id = EXCLUDED.to_id,
				type = EXCLUDED.type, weight = EXCLUDED.weight, metadata = EXCLUDED.metadata`,
			r.ID, r.FromID, r.ToID, r.Type, r.Weight, meta)
		if err != nil {
			errs = append(errs, fmt.Errorf("upsert relation %s: %w", r.ID, err))
			continue
		}
		rCount++
	}

	return eCount, rCount, errs
}

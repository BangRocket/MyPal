// Package migrate_memory implements the "migrate-memory" subcommand.
//
// It reads vector memories (and optionally graph data) from a mem0
// PostgreSQL/pgvector database or a mypalclara export archive, and writes
// them into MyPal's configured backends (Qdrant, pgvector, FalkorDB).
//
// # License
// See LICENSE in the root of the repository.
package migrate_memory

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Command returns the cobra command for the "migrate-memory" subcommand.
func Command() *cobra.Command {
	var (
		sourceDSN  string
		sourceType string
		targetDSN  string
		batchSize  int
		dryRun     bool

		// clara-export flags
		archive          string
		qdrantEndpoint   string
		qdrantCollection string
		qdrantAPIKey     string
		falkordbAddr     string
		falkordbPassword string
		falkordbGraph    string
		userID           string
	)

	cmd := &cobra.Command{
		Use:   "migrate-memory",
		Short: "Migrate memory data into MyPal",
		Long: `Reads vector memories and graph data from a mem0 PostgreSQL/pgvector
database or a mypalclara export archive, and writes them into MyPal's
configured memory backends.

Source types:
  mem0           Read directly from a mem0 PostgreSQL database
  clara-export   Read from a mypalclara export archive (.tar.gz)

Examples:

  # From mem0 database:
  mypal migrate-memory \
    --source-dsn "postgres://user:pass@localhost:5432/mem0" \
    --target-dsn "postgres://user:pass@localhost:5432/mypal"

  # From mypalclara export archive:
  mypal migrate-memory --source-type clara-export \
    --archive ./clara-export-20260320.tar.gz \
    --qdrant-endpoint http://localhost:6333 \
    --qdrant-collection mypal_memories \
    --falkordb-addr localhost:6380 \
    --falkordb-graph mypal_memory

  # Dry run (show counts only):
  mypal migrate-memory --source-type clara-export \
    --archive ./clara-export-20260320.tar.gz --dry-run`,

		RunE: func(cmd *cobra.Command, args []string) error {
			switch sourceType {
			case "mem0":
				if sourceDSN == "" {
					return fmt.Errorf("--source-dsn is required for source-type \"mem0\"")
				}
				if targetDSN == "" {
					return fmt.Errorf("--target-dsn is required for source-type \"mem0\"")
				}
				return runMem0Migration(cmd.Context(), migrationConfig{
					SourceDSN: sourceDSN,
					TargetDSN: targetDSN,
					BatchSize: batchSize,
					DryRun:    dryRun,
				})

			case "clara-export":
				if archive == "" {
					return fmt.Errorf("--archive is required for source-type \"clara-export\"")
				}
				return runClaraExportMigration(cmd.Context(), claraExportConfig{
					ArchivePath:      archive,
					QdrantEndpoint:   qdrantEndpoint,
					QdrantCollection: qdrantCollection,
					QdrantAPIKey:     qdrantAPIKey,
					TargetDSN:        targetDSN,
					FalkorDBAddr:     falkordbAddr,
					FalkorDBPassword: falkordbPassword,
					FalkorDBGraph:    falkordbGraph,
					BatchSize:        batchSize,
					DryRun:           dryRun,
					UserID:           userID,
				})

			default:
				return fmt.Errorf("unsupported --source-type %q (use \"mem0\" or \"clara-export\")", sourceType)
			}
		},
	}

	// Common flags.
	cmd.Flags().StringVar(&sourceType, "source-type", "mem0", "source system type: \"mem0\" or \"clara-export\"")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "PostgreSQL connection string for pgvector target")
	cmd.Flags().IntVar(&batchSize, "batch-size", 500, "number of records to insert per batch")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read source and print counts without writing to target")

	// mem0 flags.
	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "PostgreSQL connection string for the mem0 source database")

	// clara-export flags.
	cmd.Flags().StringVar(&archive, "archive", "", "path to mypalclara export archive (.tar.gz)")
	cmd.Flags().StringVar(&qdrantEndpoint, "qdrant-endpoint", "", "Qdrant REST endpoint (e.g. http://localhost:6333)")
	cmd.Flags().StringVar(&qdrantCollection, "qdrant-collection", "mypal_memories", "Qdrant collection name")
	cmd.Flags().StringVar(&qdrantAPIKey, "qdrant-api-key", "", "Qdrant API key (optional)")
	cmd.Flags().StringVar(&falkordbAddr, "falkordb-addr", "", "FalkorDB address (e.g. localhost:6380)")
	cmd.Flags().StringVar(&falkordbPassword, "falkordb-password", "", "FalkorDB password (optional)")
	cmd.Flags().StringVar(&falkordbGraph, "falkordb-graph", "mypal_memory", "FalkorDB graph name")
	cmd.Flags().StringVar(&userID, "user", "", "only import records for this user ID (optional)")

	return cmd
}

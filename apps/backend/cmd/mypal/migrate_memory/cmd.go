// Package migrate_memory implements the "migrate-memory" subcommand.
//
// It reads vector memories (and optionally graph data) from a mem0
// PostgreSQL/pgvector database and writes them into MyPal's schema.
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
	)

	cmd := &cobra.Command{
		Use:   "migrate-memory",
		Short: "Migrate memory data from mem0 to MyPal",
		Long: `Reads vector memories and graph data from a mem0 PostgreSQL/pgvector
database and inserts them into MyPal's memory_vectors table.

The migration is idempotent: rows are upserted by ID, so re-running
the command is safe and will update any changed records.

Example:
  mypal migrate-memory \
    --source-dsn "postgres://user:pass@localhost:5432/mem0" \
    --target-dsn "postgres://user:pass@localhost:5432/mypal"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceType != "mem0" {
				return fmt.Errorf("unsupported --source-type %q (only \"mem0\" is supported)", sourceType)
			}

			cfg := migrationConfig{
				SourceDSN: sourceDSN,
				TargetDSN: targetDSN,
				BatchSize: batchSize,
				DryRun:    dryRun,
			}

			return runMem0Migration(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "PostgreSQL connection string for the mem0 database (required)")
	cmd.Flags().StringVar(&sourceType, "source-type", "mem0", "source system type (currently only \"mem0\")")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "PostgreSQL connection string for the MyPal database (required)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 500, "number of rows to insert per batch")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "read source and print counts without writing to target")

	_ = cmd.MarkFlagRequired("source-dsn")
	_ = cmd.MarkFlagRequired("target-dsn")

	return cmd
}

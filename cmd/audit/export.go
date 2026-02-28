package audit

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newExportCommand returns the `audit export` subcommand.
func newExportCommand() *cobra.Command {
	var (
		sessionID  string
		from       string
		to         string
		outputPath string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export audit log records as JSONL",
		Long: `Export signed audit log records as newline-delimited JSON (JSONL).

Each line is one complete signed record. The output is pipe-able directly
into 'truebearing audit verify' or archivable to a file with --output.

Examples:
  truebearing audit export | truebearing audit verify
  truebearing audit export --session sess-abc123 --output audit.jsonl
  truebearing audit export --from 2026-01-01T00:00:00Z --output jan.jsonl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := buildAuditFilter(sessionID, "", "", "", from, to)
			if err != nil {
				return err
			}

			dbPath := resolveQueryDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() { _ = st.Close() }()

			// Design: when --output is given we create the named file and write
			// there, giving operators a replayable archive. When omitted, we write
			// to cmd's stdout so the command is pipeline-friendly:
			//   audit export | audit verify
			var w io.Writer
			if outputPath != "" {
				f, ferr := os.Create(outputPath)
				if ferr != nil {
					return fmt.Errorf("creating output file %s: %w", outputPath, ferr)
				}
				defer func() { _ = f.Close() }()
				w = f
			} else {
				w = cmd.OutOrStdout()
			}

			return writeExport(st, filter, w)
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "filter by session ID")
	cmd.Flags().StringVar(&from, "from", "", "start of time range inclusive (RFC3339, e.g. 2026-01-01T00:00:00Z)")
	cmd.Flags().StringVar(&to, "to", "", "end of time range inclusive (RFC3339)")
	cmd.Flags().StringVar(&outputPath, "output", "", "write JSONL output to this file path (default: stdout)")

	return cmd
}

// writeExport queries audit records matching filter from st and writes them to
// w as JSONL (one signed JSON object per line). The output is consumable
// directly by 'truebearing audit verify' without any intermediate conversion.
func writeExport(st *store.Store, filter store.AuditFilter, w io.Writer) error {
	records, err := st.QueryAuditLog(filter)
	if err != nil {
		return fmt.Errorf("querying audit log for export: %w", err)
	}
	// writeQueryJSON produces JSONL: json.Encoder appends a newline after
	// each Encode call, so each record occupies exactly one line with no
	// trailing comma — the format audit verify reads line by line.
	return writeQueryJSON(records, w)
}

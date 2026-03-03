package policy

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// ANSI terminal escape codes for lint severity colouring. These are standard
// sequences supported by all POSIX-compliant terminals and modern Windows terminals.
// They are written directly to the output writer; no external terminal library is needed.
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

// newLintCommand returns the `policy lint` subcommand.
func newLintCommand() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:   "lint <file>",
		Short: "Check a policy file for common mistakes",
		Long: `Run the TrueBearing policy linter against a YAML file and report
any rule violations (see mvp-plan.md §6.4 for the full rule table).

Exit code 0 if no ERRORs; exit code 1 if any ERROR rules fire.
WARNINGs and INFOs are printed but do not affect the exit code.

When --db is provided, the linter also runs rule L020: if the policy uses
enforcement_mode: block but has no audit history for its fingerprint, a
WARNING is emitted suggesting a shadow-mode trial first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := policy.ParseFile(args[0])
			if err != nil {
				return err
			}
			results := policy.Lint(p)

			// L020 requires audit history from the store. Skip silently when
			// --db is not provided so the linter remains usable without a
			// running proxy. When --db is provided, a missing or unreadable
			// database is a hard error — the caller explicitly asked for the
			// deployment-history check.
			if dbPath != "" {
				st, err := store.Open(dbPath)
				if err != nil {
					return fmt.Errorf("opening database for L020 check: %w", err)
				}
				defer st.Close()

				hasHistory, err := st.HasAuditRecordsForFingerprint(p.Fingerprint)
				if err != nil {
					return fmt.Errorf("querying audit history for L020 check: %w", err)
				}
				results = append(results, policy.LintL020(p, hasHistory)...)
			}

			errCount := printLintResults(cmd.OutOrStdout(), results)
			if errCount > 0 {
				return fmt.Errorf("%d error(s) found", errCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dbPath, "db", "", "Path to TrueBearing SQLite database (enables L020: block-mode deployment history check)")
	return cmd
}

// printLintResults writes coloured lint diagnostics to w and returns the number
// of ERROR-severity results. It writes nothing when results is empty.
//
// Design: the result list is already ordered by rule code (L001 first, L013 last)
// because Lint() appends them in rule order. We preserve that order here.
func printLintResults(w io.Writer, results []policy.LintResult) int {
	errCount := 0
	for _, r := range results {
		color := ansiCyan
		switch r.Severity {
		case policy.SeverityError:
			color = ansiRed
			errCount++
		case policy.SeverityWarning:
			color = ansiYellow
		}
		fmt.Fprintf(w, "%s%s [%s]%s %s\n", color, r.Code, r.Severity, ansiReset, r.Message)
	}
	return errCount
}

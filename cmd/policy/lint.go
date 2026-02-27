package policy

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/policy"
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
	return &cobra.Command{
		Use:   "lint <file>",
		Short: "Check a policy file for common mistakes",
		Long: `Run the TrueBearing policy linter against a YAML file and report
any rule violations (see mvp-plan.md §6.4 for the full rule table).

Exit code 0 if no ERRORs; exit code 1 if any ERROR rules fire.
WARNINGs and INFOs are printed but do not affect the exit code.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := policy.ParseFile(args[0])
			if err != nil {
				return err
			}
			results := policy.Lint(p)
			errCount := printLintResults(cmd.OutOrStdout(), results)
			if errCount > 0 {
				return fmt.Errorf("%d error(s) found", errCount)
			}
			return nil
		},
	}
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

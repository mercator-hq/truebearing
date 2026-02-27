package audit

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVerifyCommand returns the `audit verify` subcommand.
// The real implementation is added in Task 5.3.
func newVerifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify Ed25519 signatures on every record in an audit log file",
		Long: `Read a JSONL audit log file and verify the Ed25519 signature on each
record using the proxy's public key. Prints OK or TAMPERED per line.
Exits non-zero if any record fails verification.`,
		Args: cobra.ExactArgs(1),
		// TODO(5.3): remove stub and implement audit verification.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: audit verify")
			return nil
		},
	}
}

package policy

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newValidateCommand returns the `policy validate` subcommand.
// The real implementation is added in Task 2.3.
func newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file>",
		Short: "Parse and validate a policy YAML file",
		Long: `Parse a policy YAML file and report any structural errors.
Exits non-zero if the file is invalid. Suitable for use in CI.`,
		Args: cobra.ExactArgs(1),
		// TODO(2.3): remove stub and implement policy validation.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("[not yet implemented]: policy validate")
			return nil
		},
	}
}

package policy

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// newValidateCommand returns the `policy validate` subcommand.
func newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file>",
		Short: "Parse and validate a policy YAML file",
		Long: `Parse a policy YAML file and report any structural errors.
Exits non-zero if the file is invalid. Suitable for use in CI.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := policy.ParseFile(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "OK")
			return nil
		},
	}
}

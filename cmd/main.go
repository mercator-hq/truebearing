// Command truebearing is the TrueBearing CLI — a transparent MCP proxy with
// sequence-aware behavioral policy enforcement.
//
// All subcommands are registered in their respective cmd/ subdirectories.
// This file is the entry point and root command; see cmd/ siblings for implementations.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mercator-hq/truebearing/cmd/agent"
	"github.com/mercator-hq/truebearing/cmd/audit"
	"github.com/mercator-hq/truebearing/cmd/escalation"
	"github.com/mercator-hq/truebearing/cmd/policy"
	"github.com/mercator-hq/truebearing/cmd/session"
)

// version is the build-time version string injected via:
//
//	-ldflags="-X github.com/mercator-hq/truebearing/cmd.version=<tag>"
//
// When built without ldflags injection (e.g. go run or a development build)
// it falls back to "dev" so that `truebearing --version` always produces
// a usable string rather than an empty one.
var version = "dev"

// Package-level flag variables bound to the root command's persistent flags.
// These are populated by cobra before PersistentPreRunE runs.
var (
	cfgFile    string
	policyFile string
	dbPath     string
)

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// newRootCommand constructs the cobra root command, registers all persistent flags,
// wires viper configuration, and adds all subcommand groups.
func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:     "truebearing",
		Version: version,
		Short:   "Sequence-aware MCP proxy with behavioral policy enforcement",
		Long: `TrueBearing intercepts MCP tool calls and enforces behavioral policies
that are sequence-aware: they know what happened before the current call.

Get started:
  truebearing agent register <name> --policy ./policy.yaml
  truebearing serve --upstream <mcp-url> --policy ./policy.yaml`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig(cmd)
		},
	}

	// Persistent flags are inherited by every subcommand.
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path (default: ~/.truebearing/config.yaml)")
	root.PersistentFlags().StringVar(&policyFile, "policy", "./truebearing.policy.yaml", "policy YAML file")
	root.PersistentFlags().StringVar(&dbPath, "db", "", "SQLite database path (default: ~/.truebearing/truebearing.db)")

	// Top-level commands that do not form a named group.
	root.AddCommand(newInitCommand())
	root.AddCommand(newServeCommand())
	root.AddCommand(newSimulateCommand())

	// Named subcommand groups.
	root.AddCommand(policy.NewCommand())
	root.AddCommand(audit.NewCommand())
	root.AddCommand(session.NewCommand())
	root.AddCommand(escalation.NewCommand())
	root.AddCommand(agent.NewCommand())

	return root
}

// initConfig loads the viper configuration from ~/.truebearing/config.yaml (or the
// explicit --config path) and merges a per-project .truebearing.yaml from the
// working directory if present. Persistent flags override config file values.
func initConfig(cmd *cobra.Command) error {
	if cfgFile != "" {
		// An explicit --config path was provided; use it directly.
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home directory: %w", err)
		}
		viper.AddConfigPath(filepath.Join(home, ".truebearing"))
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Allow environment variables of the form TRUEBEARING_<KEY> to override config values.
	viper.SetEnvPrefix("TRUEBEARING")
	viper.AutomaticEnv()

	// Bind persistent flags so that flag values take precedence over the config file.
	root := cmd.Root()
	if err := viper.BindPFlag("policy", root.PersistentFlags().Lookup("policy")); err != nil {
		return fmt.Errorf("binding policy flag: %w", err)
	}
	if err := viper.BindPFlag("db", root.PersistentFlags().Lookup("db")); err != nil {
		return fmt.Errorf("binding db flag: %w", err)
	}

	// Read the primary config file. A missing file is acceptable; any other
	// read error (malformed YAML, bad permissions) is fatal.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("reading config: %w", err)
		}
	}

	// Design: merge per-project .truebearing.yaml from the working directory using
	// a separate viper instance so that the global config search paths are not
	// modified. Per-project values override the global config but are themselves
	// overridden by explicit flags.
	perProject := viper.New()
	perProject.SetConfigName(".truebearing")
	perProject.SetConfigType("yaml")
	perProject.AddConfigPath(".")
	if err := perProject.ReadInConfig(); err == nil {
		if err := viper.MergeConfigMap(perProject.AllSettings()); err != nil {
			return fmt.Errorf("merging per-project config: %w", err)
		}
	}

	return nil
}

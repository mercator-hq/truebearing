package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mercator-hq/truebearing/internal/identity"
	inpolicy "github.com/mercator-hq/truebearing/internal/policy"
	"github.com/mercator-hq/truebearing/internal/proxy"
	"github.com/mercator-hq/truebearing/internal/store"
)

// newServeCommand returns the `truebearing serve` command.
func newServeCommand() *cobra.Command {
	var (
		upstream     string
		port         int
		captureTrace string
		stdio        bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the TrueBearing proxy server",
		Long: `Start the TrueBearing MCP proxy on the configured port.

The proxy intercepts all MCP tool calls, evaluates them against the loaded
policy, and forwards allowed calls to the upstream MCP server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if upstream == "" {
				return fmt.Errorf("--upstream flag is required")
			}
			if stdio {
				return fmt.Errorf("--stdio mode is not yet implemented")
			}

			// Parse the upstream URL early so a malformed value is caught before
			// any port is bound or database is opened.
			upstreamURL, err := url.Parse(upstream)
			if err != nil {
				return fmt.Errorf("parsing --upstream %q: %w", upstream, err)
			}

			// Load and validate the policy file. Fail fast if it is absent or
			// invalid so the operator sees a clear error before traffic starts.
			policyPath := viper.GetString("policy")
			pol, err := inpolicy.ParseFile(policyPath)
			if err != nil {
				return fmt.Errorf("loading policy from %s: %w", policyPath, err)
			}

			// Open the database. The default path is ~/.truebearing/truebearing.db
			// unless --db was supplied.
			dbPath := serveResolveDBPath()
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening database at %s: %w", dbPath, err)
			}
			defer func() {
				if cerr := st.Close(); cerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: closing database: %v\n", cerr)
				}
			}()

			// Load the proxy signing key used to sign audit records. If the key
			// file is absent (e.g. first run before `agent register` has been
			// called for the proxy), audit records are not persisted and a warning
			// is printed. The proxy continues to operate normally without signing.
			keyPath := proxySigningKeyPath()
			signingKey, keyErr := identity.LoadPrivateKey(keyPath)
			if keyErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not load proxy signing key from %s — audit records will not be signed\n  run: truebearing agent register proxy --policy %s\n", keyPath, policyPath)
			}

			p := proxy.New(upstreamURL, st, pol, dbPath, signingKey)

			// Open the trace capture file if --capture-trace was set. The file is
			// opened in append mode so a proxy restart does not truncate prior
			// captures from the same session. The writer is closed on return so
			// the OS flushes any remaining kernel buffers.
			if captureTrace != "" {
				tw, twErr := proxy.NewTraceWriter(captureTrace)
				if twErr != nil {
					return fmt.Errorf("opening capture-trace file %s: %w", captureTrace, twErr)
				}
				defer func() {
					if cerr := tw.Close(); cerr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: closing trace file: %v\n", cerr)
					}
				}()
				p.SetTraceWriter(tw)
			}

			addr := fmt.Sprintf(":%d", port)
			fmt.Fprintf(cmd.OutOrStdout(), "TrueBearing proxy\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  listening on  %s\n", addr)
			fmt.Fprintf(cmd.OutOrStdout(), "  upstream      %s\n", upstream)
			fmt.Fprintf(cmd.OutOrStdout(), "  policy        %s  (%s)\n", policyPath, pol.ShortFingerprint())
			fmt.Fprintf(cmd.OutOrStdout(), "  db            %s\n", dbPath)
			if captureTrace != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  capture-trace %s\n", captureTrace)
			}

			if err := http.ListenAndServe(addr, p.Handler()); err != nil {
				return fmt.Errorf("proxy server: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&upstream, "upstream", "", "upstream MCP server URL (required)")
	cmd.Flags().IntVar(&port, "port", 7773, "local port to listen on")
	cmd.Flags().StringVar(&captureTrace, "capture-trace", "", "write all MCP traffic to a JSONL trace file")
	cmd.Flags().BoolVar(&stdio, "stdio", false, "accept MCP requests on stdin/stdout instead of HTTP")

	return cmd
}

// serveResolveDBPath returns the SQLite database path for the serve command.
// It honours the --db flag (via viper) and falls back to ~/.truebearing/truebearing.db.
func serveResolveDBPath() string {
	if db := viper.GetString("db"); db != "" {
		return db
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "truebearing.db"
	}
	return filepath.Join(home, ".truebearing", "truebearing.db")
}

// proxySigningKeyPath returns the default path for the proxy's Ed25519 private
// key used to sign audit records. Per mvp-plan.md Appendix B, the proxy signing
// key is stored at ~/.truebearing/keys/proxy.pem.
func proxySigningKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("keys", "proxy.pem")
	}
	return filepath.Join(home, ".truebearing", "keys", "proxy.pem")
}

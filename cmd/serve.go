package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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
		otelEndpoint string
		logLevel     string
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

			// Initialise structured JSON logging. The handler writes to stderr so
			// that log output is separate from the human-readable startup banner on
			// stdout and can be piped independently (e.g. truebearing serve 2>&1 | jq .).
			// The log level is parsed from --log-level (default: info). At debug level
			// the engine pipeline logs each evaluator's result in order, enabling
			// fine-grained policy diagnosis without recompiling.
			level, levelErr := parseLogLevel(logLevel)
			if levelErr != nil {
				return levelErr
			}
			logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
			slog.SetDefault(logger)
			p.SetLogger(logger)

			// Initialise OTel tracing. If --otel-endpoint is set (or
			// OTEL_EXPORTER_OTLP_ENDPOINT is in the environment), spans are
			// emitted per tool-call decision to the configured collector.
			// InitTracer fails open: a missing or unreachable endpoint returns
			// a no-op tracer so enforcement is unaffected.
			tracer, otelShutdown, otelErr := proxy.InitTracer(otelEndpoint)
			if otelErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: OTel tracer init failed — running without tracing: %v\n", otelErr)
			} else if otelEndpoint != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  otel-endpoint  %s\n", otelEndpoint)
			}
			p.SetTracer(tracer)
			defer otelShutdown(context.Background())

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

			// Listen for SIGHUP and reload the policy file atomically on receipt.
			// A failed reload (parse error or lint ERROR) leaves the previous
			// policy active; the error is logged so the operator can fix and retry.
			// The goroutine exits when the context is cancelled (when serve returns),
			// so it does not outlive the proxy.
			sighupCh := make(chan os.Signal, 1)
			signal.Notify(sighupCh, syscall.SIGHUP)
			go func() {
				for {
					select {
					case <-sighupCh:
						if reloadErr := p.ReloadPolicy(); reloadErr != nil {
							logger.Error("policy reload failed — previous policy remains active",
								"error", reloadErr,
							)
						} else {
							logger.Info("policy reloaded",
								"path", policyPath,
								"fingerprint", p.Policy().ShortFingerprint(),
							)
						}
					case <-cmd.Context().Done():
						signal.Stop(sighupCh)
						return
					}
				}
			}()

			if stdio {
				// In stdio mode, stdout is reserved for JSON-RPC protocol messages.
				// Print startup diagnostics to stderr so they do not pollute the
				// output stream consumed by the MCP client.
				jwtToken := os.Getenv("TRUEBEARING_AGENT_JWT")
				if jwtToken == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: TRUEBEARING_AGENT_JWT is not set — all tool calls will be denied\n")
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "TrueBearing proxy (stdio)\n")
				fmt.Fprintf(cmd.ErrOrStderr(), "  upstream      %s\n", upstream)
				fmt.Fprintf(cmd.ErrOrStderr(), "  policy        %s  (%s)\n", policyPath, pol.ShortFingerprint())
				fmt.Fprintf(cmd.ErrOrStderr(), "  db            %s\n", dbPath)
				if captureTrace != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "  capture-trace %s\n", captureTrace)
				}
				return p.ServeStdio(cmd.Context(), os.Stdin, os.Stdout, jwtToken)
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
	cmd.Flags().StringVar(&otelEndpoint, "otel-endpoint", "", "OTLP HTTP endpoint for trace emission (e.g. http://localhost:4318); overrides OTEL_EXPORTER_OTLP_ENDPOINT")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log verbosity: debug, info, warn, error")

	return cmd
}

// parseLogLevel converts a --log-level string to a slog.Level value. The four
// accepted levels mirror the standard slog levels. "warning" is accepted as an
// alias for "warn" to match common operator muscle memory.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q: must be one of debug, info, warn, error", s)
	}
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

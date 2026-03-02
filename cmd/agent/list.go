package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mercator-hq/truebearing/internal/store"
)

// newListCommand returns the `agent list` subcommand.
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered agents",
		Long: `Show all registered agents: name, registration date, policy file,
allowed tool count, JWT expiry, and revocation status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}
}

// runList implements truebearing agent list.
func runList() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	tbHome := home + "/.truebearing"

	dbPath := resolveDBPath(tbHome)
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer st.Close()

	agents, err := st.ListAgents()
	if err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}

	if len(agents) == 0 {
		fmt.Println("No registered agents.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPOLICY FILE\tTOOLS\tREGISTERED\tEXPIRES\tSTATUS")
	for _, a := range agents {
		toolCount := "?"
		if tools, err := a.AllowedTools(); err == nil {
			toolCount = fmt.Sprintf("%d", len(tools))
		}

		registeredAt := time.Unix(0, a.RegisteredAt).Format("2006-01-02 15:04")

		expiresAt := "unknown"
		if t, ok := jwtExpiry(a.JWTPreview); ok {
			expiresAt = t.Format("2006-01-02 15:04")
		}

		status := "active"
		if a.IsRevoked() {
			revokedTime := time.Unix(0, *a.RevokedAt).Format("2006-01-02 15:04")
			status = fmt.Sprintf("REVOKED %s", revokedTime)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			a.Name, a.PolicyFile, toolCount, registeredAt, expiresAt, status)
	}
	return w.Flush()
}

// jwtExpiry extracts the expiry time from a JWT without verifying its signature.
// JWT payloads use base64url encoding without padding (RFC 7519 §2).
// Returns the zero time and false if extraction fails for any reason.
func jwtExpiry(tokenStr string) (time.Time, bool) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}

	// Decode the payload segment (second of three dot-separated parts).
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}

	var claims struct {
		Exp int64 `json:"exp"` // JWT NumericDate is seconds since Unix epoch (RFC 7519 §2).
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return time.Time{}, false
	}
	if claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

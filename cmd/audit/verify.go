package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	internalaudit "github.com/mercator-hq/truebearing/internal/audit"
	"github.com/mercator-hq/truebearing/internal/identity"
)

// newVerifyCommand returns the `audit verify` subcommand.
func newVerifyCommand() *cobra.Command {
	var keyPath string

	cmd := &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify Ed25519 signatures on every record in an audit log file",
		Long: `Read a JSONL audit log file and verify the Ed25519 signature on each
record using the proxy's public key. Prints OK or TAMPERED per line.
Exits non-zero if any record fails verification.

The --key flag must point to the Ed25519 public key (.pub.pem) that was
active when the audit records were signed. This defaults to the proxy's
key at ~/.truebearing/keys/proxy.pub.pem.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(args[0], keyPath, cmd)
		},
	}

	cmd.Flags().StringVar(&keyPath, "key", defaultProxyPubKeyPath(), "path to the Ed25519 public key used for verification (.pub.pem)")

	return cmd
}

// runVerify implements `truebearing audit verify`. It loads the public key,
// reads the JSONL audit log line by line, verifies each record's Ed25519
// signature, and prints OK or TAMPERED per record. It exits non-zero if any
// record fails verification or cannot be parsed.
func runVerify(filePath, keyPath string, cmd *cobra.Command) error {
	pubKey, err := identity.LoadPublicKey(keyPath)
	if err != nil {
		return fmt.Errorf("loading public key from %s: %w", keyPath, err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening audit log file %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// Increase the per-line buffer to 1 MiB so large JSON records (long
	// decision reasons, long trace IDs) do not overflow the default 64 KiB.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var total, tampered int
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		total++

		var rec internalaudit.AuditRecord
		if parseErr := json.Unmarshal([]byte(line), &rec); parseErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "PARSE_ERROR  line %d: %v\n", total, parseErr)
			tampered++
			continue
		}

		// Per-record label: abbreviated ID, sequence number, and tool name.
		label := fmt.Sprintf("id=%.8s  seq=%-6d  tool=%s", rec.ID, rec.Seq, rec.ToolName)

		if verr := internalaudit.Verify(&rec, pubKey); verr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "TAMPERED  %s\n", label)
			tampered++
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "OK        %s\n", label)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading audit log file %s: %w", filePath, err)
	}

	if total == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no records found in file)")
		return nil
	}

	ok := total - tampered
	fmt.Fprintf(cmd.OutOrStdout(), "\n%d OK, %d TAMPERED (out of %d records)\n", ok, tampered, total)

	if tampered > 0 {
		return fmt.Errorf("%d record(s) failed signature verification", tampered)
	}
	return nil
}

// defaultProxyPubKeyPath returns the default path for the proxy's Ed25519
// public key. Per mvp-plan.md Appendix B, the proxy signing key is stored at
// ~/.truebearing/keys/proxy.pub.pem.
func defaultProxyPubKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to a relative path if the home directory cannot be resolved.
		return "proxy.pub.pem"
	}
	return filepath.Join(home, ".truebearing", "keys", "proxy.pub.pem")
}

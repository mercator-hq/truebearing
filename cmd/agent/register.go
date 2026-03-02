package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/mercator-hq/truebearing/internal/identity"
	"github.com/mercator-hq/truebearing/internal/store"
)

// minimalPolicy extracts only the fields needed for agent registration.
// The full policy parser lives in internal/policy (Task 2.1). This struct
// is intentionally minimal — Phase 1 only needs may_use to populate the JWT.
type minimalPolicy struct {
	MayUse []string `yaml:"may_use"`
}

// newRegisterCommand returns the `agent register` subcommand.
func newRegisterCommand() *cobra.Command {
	var expiryDays int
	var env string
	var parentName string

	cmd := &cobra.Command{
		Use:   "register <name>",
		Short: "Register a new agent and issue its credentials",
		Long: `Generate an Ed25519 keypair for the named agent, issue a signed JWT
bound to the specified policy, and write both to ~/.truebearing/keys/.

The JWT is scoped to the tools listed in the policy's may_use field.
Re-registering an existing agent name overwrites its credentials.

Use --env to bind the agent to a specific deployment environment (e.g.
"production", "staging"). When the policy declares require_env, only agents
whose --env value matches are permitted to make tool calls in that session.

Use --parent to register a child agent whose delegation scope is bounded by
the parent's allowed tool set. The child's may_use must be a subset of the
parent's allowed tools; registration fails if this constraint is violated,
preventing child agents from escalating their own permissions beyond the
parent's scope.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegister(args[0], expiryDays, env, parentName)
		},
	}

	cmd.Flags().IntVar(&expiryDays, "expiry-days", 365, "JWT validity period in days")
	cmd.Flags().StringVar(&env, "env", "", `Deployment environment for this agent (e.g. "production", "staging"). Embedded in the JWT "env" claim and enforced by the policy require_env field.`)
	cmd.Flags().StringVar(&parentName, "parent", "", `Parent agent name. The new agent becomes a child of the named parent and is denied calls to any tool not in the parent's allowed set. Registration fails if the child's may_use contains tools outside the parent's scope.`)

	return cmd
}

// runRegister implements truebearing agent register.
func runRegister(name string, expiryDays int, env, parentName string) error {
	policyFile := viper.GetString("policy")

	// Validate policy file exists and is readable before doing any key generation.
	data, err := os.ReadFile(policyFile)
	if err != nil {
		return fmt.Errorf("reading policy file %s: %w", policyFile, err)
	}

	// Minimal YAML parse: extract only may_use. The full parser is Phase 2 (Task 2.1).
	var pol minimalPolicy
	if err := yaml.Unmarshal(data, &pol); err != nil {
		return fmt.Errorf("parsing policy YAML %s: %w", policyFile, err)
	}

	// Normalise nil may_use to an empty slice so JSON encodes as "[]" not "null".
	if pol.MayUse == nil {
		pol.MayUse = []string{}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	tbHome := filepath.Join(home, ".truebearing")

	// Open the store early so we can validate the parent before generating keys.
	dbPath := resolveDBPath(tbHome)
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database at %s: %w", dbPath, err)
	}
	defer st.Close()

	// If --parent is set, load the parent agent and validate that every tool in
	// this child's may_use is within the parent's allowed set. Child agents cannot
	// exceed parent permissions — the check here prevents privilege escalation at
	// registration time (before any JWT is issued).
	var parentAllowed []string
	if parentName != "" {
		parent, err := st.GetAgent(parentName)
		if err != nil {
			return fmt.Errorf("loading parent agent %q: %w", parentName, err)
		}
		parentAllowed, err = parent.AllowedTools()
		if err != nil {
			return fmt.Errorf("decoding allowed tools for parent agent %q: %w", parentName, err)
		}

		// Build a set of the parent's allowed tools for O(1) membership checks.
		parentSet := make(map[string]struct{}, len(parentAllowed))
		for _, t := range parentAllowed {
			parentSet[t] = struct{}{}
		}

		// Collect every child tool that exceeds the parent's scope.
		var exceeds []string
		for _, t := range pol.MayUse {
			if _, ok := parentSet[t]; !ok {
				exceeds = append(exceeds, t)
			}
		}
		if len(exceeds) > 0 {
			return fmt.Errorf(
				"child agent %q cannot be registered with tools outside parent %q's allowed set: [%s]\n"+
					"parent's allowed tools: [%s]",
				name, parentName,
				strings.Join(exceeds, ", "),
				strings.Join(parentAllowed, ", "),
			)
		}
	}

	// Generate the Ed25519 keypair. This also creates ~/.truebearing/keys/ if absent.
	_, privKey, err := identity.GenerateKeypair(name, tbHome)
	if err != nil {
		return fmt.Errorf("generating keypair for agent %q: %w", name, err)
	}

	// Mint the JWT with AllowedTools populated from may_use, Env set when
	// the --env flag was provided, and ParentAgent + ParentAllowed set when
	// the --parent flag was provided. The DelegationEvaluator in the proxy
	// reads parent_agent from the JWT on every tool call to enforce scope.
	claims := identity.AgentClaims{
		AgentName:     name,
		PolicyFile:    policyFile,
		AllowedTools:  pol.MayUse,
		Env:           env,
		ParentAgent:   parentName,
		ParentAllowed: parentAllowed,
	}
	token, err := identity.MintAgentJWT(claims, privKey, time.Duration(expiryDays)*24*time.Hour)
	if err != nil {
		return fmt.Errorf("minting JWT for agent %q: %w", name, err)
	}

	// Write JWT to ~/.truebearing/keys/<name>.jwt with 0600 permissions.
	// GenerateKeypair has already created the keys/ directory.
	keysDir := filepath.Join(tbHome, "keys")
	jwtPath := filepath.Join(keysDir, name+".jwt")
	if err := os.WriteFile(jwtPath, []byte(token), 0600); err != nil {
		return fmt.Errorf("writing JWT to %s: %w", jwtPath, err)
	}

	// Read back the public key PEM that GenerateKeypair wrote to disk.
	pubPEMPath := filepath.Join(keysDir, name+".pub.pem")
	pubPEMData, err := os.ReadFile(pubPEMPath)
	if err != nil {
		return fmt.Errorf("reading public key from %s: %w", pubPEMPath, err)
	}

	// JSON-encode the allowed tools list for database storage.
	toolsJSON, err := json.Marshal(pol.MayUse)
	if err != nil {
		return fmt.Errorf("encoding allowed tools: %w", err)
	}

	agentRow := &store.Agent{
		Name:             name,
		PublicKeyPEM:     string(pubPEMData),
		PolicyFile:       policyFile,
		AllowedToolsJSON: string(toolsJSON),
		RegisteredAt:     time.Now().UnixNano(),
		JWTPreview:       token,
	}
	if err := st.UpsertAgent(agentRow); err != nil {
		return fmt.Errorf("storing agent in database: %w", err)
	}

	// Print the success summary format from mvp-plan.md §13.
	fmt.Printf("Agent:          %s\n", name)
	fmt.Printf("Public key:     %s\n", pubPEMPath)
	fmt.Printf("JWT written to: %s\n", jwtPath)
	if parentName != "" {
		fmt.Printf("Parent agent:   %s\n", parentName)
	}
	if env != "" {
		fmt.Printf("Environment:    %s\n", env)
	}
	if len(pol.MayUse) > 0 {
		fmt.Printf("Allowed tools (%d from policy may_use): [%s]\n",
			len(pol.MayUse), strings.Join(pol.MayUse, ", "))
	} else {
		fmt.Println("Allowed tools: (none — may_use is empty in policy)")
	}
	fmt.Printf("\nTo use:\n")
	fmt.Printf("  export TRUEBEARING_AGENT_JWT=$(cat %s)\n", jwtPath)
	fmt.Printf("  OR pass --agent-jwt flag to your client\n")

	return nil
}

// resolveDBPath returns the database file path from viper config or the default.
// tbHome is the ~/.truebearing directory, used to construct the default path.
func resolveDBPath(tbHome string) string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	return filepath.Join(tbHome, "truebearing.db")
}

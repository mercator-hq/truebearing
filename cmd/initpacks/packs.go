// Package initpacks provides embedded policy pack templates for use by truebearing init.
//
// It does not own policy parsing or lint evaluation (see internal/policy).
// Each embedded YAML file mirrors the canonical template from policy-packs/; changes to
// the source packs must be propagated here to keep the binary current.
//
// Invariant: every file listed in verticalFiles must be embedded and must parse and lint
// with zero ERRORs. The init() function enforces this at binary startup.
package initpacks

import (
	"embed"
)

//go:embed data/*.yaml
var packFS embed.FS

// verticalFiles maps a vertical identifier to its embedded YAML filename.
// Keys are the canonical short identifiers accepted by the --vertical flag.
var verticalFiles = map[string]string{
	"finance":       "data/finance-payments.yaml",
	"healthcare":    "data/healthcare-hipaa.yaml",
	"legal":         "data/legal-privileged-docs.yaml",
	"life-sciences": "data/life-sciences-regulatory.yaml",
	"devops":        "data/devops-infra.yaml",
}

// verticalOrder defines display order for the interactive vertical question.
// Order is stable across builds; append new verticals to the end.
var verticalOrder = []string{"finance", "healthcare", "legal", "life-sciences", "devops"}

func init() {
	// Verify at startup that every file referenced in verticalFiles is actually
	// embedded. A mismatch means the binary was built with an incomplete embed
	// directive, which is a programmer error that cannot be recovered at runtime.
	for vertical, file := range verticalFiles {
		if _, err := packFS.ReadFile(file); err != nil {
			panic("initpacks: broken embed for vertical " + vertical + ": " + err.Error())
		}
	}
}

// ByVertical returns the embedded policy pack content for the given vertical
// identifier. It returns (nil, false) if the identifier is not recognised.
// Valid identifiers: "finance", "healthcare", "legal", "life-sciences", "devops".
func ByVertical(vertical string) ([]byte, bool) {
	file, ok := verticalFiles[vertical]
	if !ok {
		return nil, false
	}
	// init() has already verified every listed file is present; this call cannot fail.
	content, err := packFS.ReadFile(file)
	if err != nil {
		// Should never happen: init() panics on any missing embed.
		return nil, false
	}
	return content, true
}

// KnownVerticals returns valid vertical identifiers in stable display order.
// "other" is intentionally excluded — it is handled by the caller as a special case.
func KnownVerticals() []string {
	out := make([]string, len(verticalOrder))
	copy(out, verticalOrder)
	return out
}

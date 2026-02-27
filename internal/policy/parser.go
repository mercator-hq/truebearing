package policy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseFile reads the YAML policy file at path, parses it, normalises nil
// collections, computes the fingerprint, and returns the Policy. p.SourcePath
// is set to path.
//
// Returns an error if the file cannot be read, the YAML is malformed, or
// required fields (version, agent) are missing.
func ParseFile(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading policy file %s: %w", path, err)
	}
	p, err := ParseBytes(data, path)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ParseBytes parses a policy from the YAML bytes in data. sourcePath is stored
// in p.SourcePath for diagnostic purposes; it does not affect the fingerprint.
//
// Returns an error if the YAML is malformed or required fields are missing.
// ParseBytes never panics on malformed input.
func ParseBytes(data []byte, sourcePath string) (*Policy, error) {
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing policy YAML from %s: %w", sourcePath, err)
	}
	if err := validate(&p, sourcePath); err != nil {
		return nil, err
	}
	normalize(&p)
	if _, err := Fingerprint(&p); err != nil {
		return nil, fmt.Errorf("computing fingerprint for %s: %w", sourcePath, err)
	}
	p.SourcePath = sourcePath
	return &p, nil
}

// validate checks that required fields are present. It does not enforce policy
// semantic rules — that is the linter's responsibility (see lint.go). validate
// only rejects policies that the parser cannot meaningfully represent.
func validate(p *Policy, sourcePath string) error {
	if p.Version == "" {
		return fmt.Errorf("policy from %s is missing required field: version", sourcePath)
	}
	if p.Agent == "" {
		return fmt.Errorf("policy from %s is missing required field: agent", sourcePath)
	}
	return nil
}

// normalize replaces nil slices and maps with their empty equivalents so that
// the JSON fingerprint is stable regardless of whether YAML fields are omitted
// or written as empty collections. Two policy files with identical semantics
// but different whitespace must hash identically.
func normalize(p *Policy) {
	if p.MayUse == nil {
		p.MayUse = []string{}
	}
	if p.Tools == nil {
		p.Tools = map[string]ToolPolicy{}
	}
	// Normalize sequence slices within each tool policy. Because map values
	// are not addressable, we must copy, mutate, and reassign.
	for name, tp := range p.Tools {
		if tp.Sequence.OnlyAfter == nil {
			tp.Sequence.OnlyAfter = []string{}
		}
		if tp.Sequence.NeverAfter == nil {
			tp.Sequence.NeverAfter = []string{}
		}
		p.Tools[name] = tp
	}
}

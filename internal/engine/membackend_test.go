package engine_test

import (
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/engine"
)

func TestMemBackend_GetSessionEvents(t *testing.T) {
	events := []engine.SessionEventEntry{
		{ToolName: "search_web", Decision: "allow", RecordedAt: 1000},
		{ToolName: "send_email", Decision: "deny", RecordedAt: 2000},
	}
	b := engine.NewMemBackend(events, nil, nil)

	got, err := b.GetSessionEvents("any-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, ev := range got {
		if ev.ToolName != events[i].ToolName || ev.Decision != events[i].Decision {
			t.Errorf("event[%d] = %+v, want %+v", i, ev, events[i])
		}
	}
}

func TestMemBackend_GetSessionEvents_empty(t *testing.T) {
	b := engine.NewMemBackend(nil, nil, nil)
	got, err := b.GetSessionEvents("session-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 events, got %d", len(got))
	}
}

func TestMemBackend_CountSessionEventsSince(t *testing.T) {
	// Five events: three allowed for "send_wire", one denied, one different tool.
	base := time.Unix(1000, 0)
	events := []engine.SessionEventEntry{
		{ToolName: "send_wire", Decision: "allow", RecordedAt: base.Add(-10 * time.Second).UnixNano()}, // outside window
		{ToolName: "send_wire", Decision: "allow", RecordedAt: base.Add(0).UnixNano()},                 // at boundary
		{ToolName: "send_wire", Decision: "allow", RecordedAt: base.Add(5 * time.Second).UnixNano()},   // inside
		{ToolName: "send_wire", Decision: "deny", RecordedAt: base.Add(6 * time.Second).UnixNano()},    // denied — must not count
		{ToolName: "read_file", Decision: "allow", RecordedAt: base.Add(7 * time.Second).UnixNano()},   // different tool
	}
	b := engine.NewMemBackend(events, nil, nil)

	// Since base: expect 2 (at boundary + inside window; denied and other tool excluded).
	count, err := b.CountSessionEventsSince("s", "send_wire", base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("got count=%d, want 2", count)
	}

	// Since base - 20s: all three allowed events are inside, denied excluded → 3.
	count2, err := b.CountSessionEventsSince("s", "send_wire", base.Add(-20*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count2 != 3 {
		t.Errorf("got count=%d, want 3", count2)
	}
}

func TestMemBackend_CountSessionEventsSince_shadowDeny(t *testing.T) {
	base := time.Unix(2000, 0)
	events := []engine.SessionEventEntry{
		{ToolName: "pay", Decision: "shadow_deny", RecordedAt: base.UnixNano()},
	}
	b := engine.NewMemBackend(events, nil, nil)
	// shadow_deny counts toward rate-limit quota like allow.
	count, err := b.CountSessionEventsSince("s", "pay", base.Add(-1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("shadow_deny should count; got %d, want 1", count)
	}
}

func TestMemBackend_HasApprovedEscalation(t *testing.T) {
	approved := []engine.ApprovedEscalationEntry{
		{ToolName: "send_wire", ArgsHash: "abc123"},
		{ToolName: "delete_record", ArgsHash: "def456"},
	}
	b := engine.NewMemBackend(nil, approved, nil)

	cases := []struct {
		tool, hash string
		want       bool
	}{
		{"send_wire", "abc123", true},
		{"send_wire", "wronghash", false},
		{"delete_record", "def456", true},
		{"nonexistent", "abc123", false},
	}
	for _, tc := range cases {
		got, err := b.HasApprovedEscalation("sess", tc.tool, tc.hash)
		if err != nil {
			t.Fatalf("[%s/%s] unexpected error: %v", tc.tool, tc.hash, err)
		}
		if got != tc.want {
			t.Errorf("[%s/%s] got %v, want %v", tc.tool, tc.hash, got, tc.want)
		}
	}
}

func TestMemBackend_GetAgentAllowedTools(t *testing.T) {
	tools := []string{"read_file", "search_web"}
	b := engine.NewMemBackend(nil, nil, tools)

	got, err := b.GetAgentAllowedTools("parent-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(tools) {
		t.Fatalf("got %d tools, want %d", len(got), len(tools))
	}
	for i, tool := range got {
		if tool != tools[i] {
			t.Errorf("tool[%d] = %q, want %q", i, tool, tools[i])
		}
	}
}

func TestMemBackend_GetAgentAllowedTools_notFound(t *testing.T) {
	b := engine.NewMemBackend(nil, nil, nil) // no parent tools
	_, err := b.GetAgentAllowedTools("parent-agent")
	if err == nil {
		t.Fatal("expected ErrParentAgentNotFound when parentTools is empty")
	}
	// The pipeline wraps this error in a Deny; verify the sentinel is propagated.
	if !isParentNotFound(err) {
		t.Errorf("error %q does not wrap ErrParentAgentNotFound", err)
	}
}

func TestMemBackend_inputsAreCopied(t *testing.T) {
	// Mutating the caller's slice after NewMemBackend must not affect the backend.
	events := []engine.SessionEventEntry{{ToolName: "a", Decision: "allow"}}
	b := engine.NewMemBackend(events, nil, nil)
	events[0].ToolName = "mutated" // mutate caller's slice

	got, _ := b.GetSessionEvents("")
	if got[0].ToolName != "a" {
		t.Errorf("MemBackend data was mutated; expected %q got %q", "a", got[0].ToolName)
	}
}

// isParentNotFound checks whether err wraps engine.ErrParentAgentNotFound.
func isParentNotFound(err error) bool {
	// Use errors.Is via the engine sentinel.
	target := engine.ErrParentAgentNotFound
	current := err
	for current != nil {
		if current == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := current.(unwrapper)
		if !ok {
			break
		}
		current = u.Unwrap()
	}
	return false
}

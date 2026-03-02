package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mercator-hq/truebearing/internal/store"
)

// TestProxy_DecisionLog_ValidJSONWithRequiredFields verifies that a successful
// tool call produces a "tool call evaluated" log entry that is valid JSON and
// contains all required fields: time, level, msg, session_id, agent, tool,
// decision, rule_id, trace_id.
//
// Per CLAUDE.md §8 security invariant 4: argument values must never appear in
// log output. This test also asserts that arguments_sha256 is present (the only
// argument-related field permitted in logs) and that the raw argument key does
// not appear in any log line.
func TestProxy_DecisionLog_ValidJSONWithRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	upstream := newTestUpstream(t, nil)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", nil)
	p.SetLogger(logger)

	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)

	const sessionID = "sess-log-test-456"
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/v1", strings.NewReader(toolsCallBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", sessionID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	// Parse each log line as JSON. Every non-empty line must parse successfully.
	logOutput := buf.String()
	var decisionEntry map[string]interface{}

	scanner := bufio.NewScanner(strings.NewReader(logOutput))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("log line is not valid JSON: %q — error: %v", line, err)
			continue
		}
		if msg, _ := entry["msg"].(string); msg == "tool call evaluated" {
			decisionEntry = entry
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		t.Fatalf("scanning log output: %v", scanErr)
	}

	if decisionEntry == nil {
		t.Fatalf("no \"tool call evaluated\" log entry found in output:\n%s", logOutput)
	}

	// Verify every required field is present in the decision log entry.
	requiredFields := []string{"time", "level", "msg", "session_id", "agent", "tool", "decision", "rule_id", "trace_id"}
	for _, field := range requiredFields {
		if _, ok := decisionEntry[field]; !ok {
			t.Errorf("required field %q missing from decision log entry", field)
		}
	}

	// Verify field values for the fields that have well-known expected values.
	if got, _ := decisionEntry["session_id"].(string); got != sessionID {
		t.Errorf("session_id: want %q, got %q", sessionID, got)
	}
	if got, _ := decisionEntry["agent"].(string); got != "test-agent" {
		t.Errorf("agent: want %q, got %q", "test-agent", got)
	}
	if got, _ := decisionEntry["tool"].(string); got != "some_tool" {
		t.Errorf("tool: want %q, got %q", "some_tool", got)
	}
	if got, _ := decisionEntry["decision"].(string); got != "allow" {
		t.Errorf("decision: want %q, got %q", "allow", got)
	}

	// arguments_sha256 must be present — the only argument-related field allowed in logs.
	if _, ok := decisionEntry["arguments_sha256"]; !ok {
		t.Error("arguments_sha256 must be present in the decision log entry")
	}

	// Security invariant 4 (CLAUDE.md §8): the raw "arguments" key must never
	// appear as a log field. Check every log line so a future regression cannot
	// slip through by appearing in a different log entry.
	for _, line := range strings.Split(logOutput, "\n") {
		if strings.Contains(line, `"arguments":`) {
			t.Errorf("raw \"arguments\" key found in log line — argument values must never be logged: %q", line)
		}
	}
}

// TestProxy_DecisionLog_DenialIncludesRuleID verifies that when a tool call is
// denied, the decision log entry records the correct decision and a non-empty
// rule_id so operators can identify which policy predicate fired.
func TestProxy_DecisionLog_DenialIncludesRuleID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	upstream := newTestUpstream(t, nil)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parsing upstream URL: %v", err)
	}

	st := store.NewTestDB(t)
	_, agentPriv := registerTestAgent(t, st, "test-agent")
	token := mintTestToken(t, agentPriv, "test-agent", time.Hour)

	pol := parseTestPolicy(t)
	p := New(upstreamURL, st, pol, "", nil)
	p.SetLogger(logger)

	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)

	// toolsCallBodyDenied calls "forbidden_tool" which is not in the policy's
	// may_use list, so MayUseEvaluator denies it.
	const sessionID = "sess-log-deny-789"
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/v1", strings.NewReader(toolsCallBodyDenied))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TrueBearing-Session-ID", sessionID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("making request: %v", err)
	}
	defer resp.Body.Close()

	// Find the decision log entry for the denied call.
	var decisionEntry map[string]interface{}
	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("log line is not valid JSON: %q", line)
			continue
		}
		if msg, _ := entry["msg"].(string); msg == "tool call evaluated" {
			decisionEntry = entry
		}
	}

	if decisionEntry == nil {
		t.Fatal("no \"tool call evaluated\" log entry found for denied call")
	}

	if got, _ := decisionEntry["decision"].(string); got != "deny" {
		t.Errorf("decision: want %q, got %q", "deny", got)
	}
	ruleID, _ := decisionEntry["rule_id"].(string)
	if ruleID == "" {
		t.Error("rule_id must be non-empty for denied calls so operators can identify the violated predicate")
	}
}

// Package proxy owns the TrueBearing HTTP reverse proxy.
// This file implements the --capture-trace file writer used by
// `truebearing serve --capture-trace <path>`.

package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// TraceEntry is a single tool call captured during a live proxy session.
// It is the JSON schema written to the JSONL trace file by
// `truebearing serve --capture-trace` and consumed by `truebearing simulate`.
//
// Design: RequestedAt is RFC3339 (not unix nanoseconds) because trace files
// are operator-readable artifacts — human-readable timestamps are easier to
// inspect, edit, and reason about than nanosecond integers.
type TraceEntry struct {
	SessionID   string          `json:"session_id"`
	AgentName   string          `json:"agent_name"`
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	RequestedAt string          `json:"requested_at"` // RFC3339
}

// TraceWriter appends tool-call trace entries to a JSONL file.
// Each entry is written as one atomic os.File.Write call so a crash does not
// produce a partial JSON line. The file is opened in append mode so proxy
// restarts do not truncate prior capture data.
//
// TraceWriter is safe for concurrent use.
type TraceWriter struct {
	mu sync.Mutex
	f  *os.File
}

// NewTraceWriter opens path for appending (creating the file if absent) and
// returns a TraceWriter ready for use. The file is created with 0600
// permissions per CLAUDE.md §8 security invariant 3. The caller must call
// Close when the proxy shuts down.
func NewTraceWriter(path string) (*TraceWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening trace file %s: %w", path, err)
	}
	return &TraceWriter{f: f}, nil
}

// WriteEntry encodes e as JSON and appends a newline-terminated line to the
// trace file. The write is protected by a mutex so concurrent goroutines (one
// per inbound HTTP request) do not interleave partial lines.
//
// Design: json.Marshal + a single f.Write is used instead of a bufio.Writer
// so there is no userspace buffering between encoding and the kernel. After
// Write returns, the data is in the kernel buffer; a goroutine panic or OOM
// kill will not lose the entry. Hardware-level durability (fsync) is not
// required for trace files, which are advisory artifacts rather than the
// authoritative audit log.
func (tw *TraceWriter) WriteEntry(e TraceEntry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding trace entry for tool %q: %w", e.ToolName, err)
	}
	// Append the newline to data before locking so we hold the mutex only for
	// the duration of the Write syscall itself.
	data = append(data, '\n')
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if _, err := tw.f.Write(data); err != nil {
		return fmt.Errorf("writing trace entry for tool %q: %w", e.ToolName, err)
	}
	return nil
}

// Close closes the underlying trace file. It must be called when the proxy
// shuts down to release the file descriptor. Close is safe to call on a nil
// *TraceWriter (it is a no-op).
func (tw *TraceWriter) Close() error {
	if tw == nil {
		return nil
	}
	return tw.f.Close()
}

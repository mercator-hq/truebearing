// Package proxy owns the TrueBearing HTTP reverse proxy.
// This file implements the --stdio transport mode used by
// `truebearing serve --stdio`.

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// maxStdioLineBytes is the maximum accepted size for a single JSON-RPC line
// read from stdin in stdio mode. MCP messages are rarely larger than a few
// kilobytes; 1 MiB is a generous upper bound that prevents unbounded memory
// growth without blocking normal usage.
const maxStdioLineBytes = 1024 * 1024 // 1 MiB

// ServeStdio runs the proxy in stdio transport mode. It reads newline-delimited
// JSON-RPC 2.0 messages from in (typically os.Stdin) and writes JSON-RPC
// responses to out (typically os.Stdout). ServeStdio blocks until in reaches
// EOF or ctx is cancelled.
//
// Auth: tokenString is the raw JWT value, typically read from the
// TRUEBEARING_AGENT_JWT environment variable by cmd/serve.go. If empty, every
// tool call is rejected with an authorization error — no bypass exists.
// Per CLAUDE.md §8: no JWT = deny, always.
//
// Session: all requests on a single stdio connection share one auto-generated
// session ID. This mirrors the Python SDK's one-session-per-PolicyProxy model:
// a stdio connection is a 1:1 process relationship and all messages on the
// stream belong to the same logical session.
//
// Design: ServeStdio reuses the existing HTTP handler chain by constructing a
// synthetic *http.Request for each JSON-RPC line and capturing the response
// with an in-memory writer. This concentrates all auth, session-ID enforcement,
// and evaluation logic in one place rather than duplicating it for the stdio
// transport.
func (p *Proxy) ServeStdio(ctx context.Context, in io.Reader, out io.Writer, tokenString string) error {
	// One session ID covers the entire stdio connection lifetime.
	sessionID := uuid.New().String()

	// Build the handler chain once; it is reused for every line on the stream.
	handler := p.Handler()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, maxStdioLineBytes), maxStdioLineBytes)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		respBytes, err := p.dispatchStdioLine(ctx, handler, line, tokenString, sessionID)
		if err != nil {
			return fmt.Errorf("stdio dispatch: %w", err)
		}

		// Each JSON-RPC response is written as one newline-terminated line.
		// A single Write call keeps the response atomic on the output stream.
		if _, err := out.Write(append(respBytes, '\n')); err != nil {
			return fmt.Errorf("writing stdio response: %w", err)
		}
	}

	return scanner.Err()
}

// dispatchStdioLine builds a synthetic *http.Request from raw JSON-RPC bytes,
// runs it through the full HTTP handler chain (auth → session → MCP router),
// and returns the captured response body. The response body is the JSON-RPC
// message to write to stdout — HTTP status codes are not transmitted over the
// stdio transport.
func (p *Proxy) dispatchStdioLine(
	ctx context.Context,
	handler http.Handler,
	line []byte,
	tokenString, sessionID string,
) ([]byte, error) {
	// Use a recognisable synthetic host; the reverse proxy Director replaces
	// the host with the upstream URL before making the forwarded HTTP call.
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://stdio.local/mcp/v1",
		bytes.NewReader(line),
	)
	if err != nil {
		return nil, fmt.Errorf("building synthetic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Auth: inject the JWT as a Bearer token so AuthMiddleware sees a standard
	// Authorization header. An empty tokenString leaves the header absent,
	// which AuthMiddleware converts to a 401-equivalent JSON error response.
	if tokenString != "" {
		req.Header.Set("Authorization", "Bearer "+tokenString)
	}

	// Session: inject the session ID header for tool calls so SessionMiddleware
	// does not reject them. Non-tool MCP methods (initialize, tools/list, ping)
	// do not require the header — SessionMiddleware only enforces it on tool calls.
	if isToolCall(line) {
		req.Header.Set(sessionIDHeader, sessionID)
	}

	rw := newStdioResponseWriter()
	handler.ServeHTTP(rw, req)
	return rw.body.Bytes(), nil
}

// stdioResponseWriter is a minimal http.ResponseWriter that buffers the
// response body for transmission over the stdio output stream. HTTP status
// codes and headers are not forwarded to the stdio caller — only the JSON body
// is written to stdout.
type stdioResponseWriter struct {
	body   bytes.Buffer
	code   int
	header http.Header
}

func newStdioResponseWriter() *stdioResponseWriter {
	return &stdioResponseWriter{
		header: make(http.Header),
		code:   http.StatusOK,
	}
}

// Header returns the response header map. Middleware may set Content-Type and
// similar headers; they are buffered here but not written to the stdio stream.
func (w *stdioResponseWriter) Header() http.Header { return w.header }

// WriteHeader records the HTTP status code. It is not transmitted over the
// stdio transport; the field is stored so middleware that inspects the code
// does not observe an inconsistent state.
func (w *stdioResponseWriter) WriteHeader(code int) { w.code = code }

// Write appends b to the response body buffer.
func (w *stdioResponseWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

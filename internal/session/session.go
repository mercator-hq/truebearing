package session

// Session is a snapshot of per-session state presented to the evaluation
// pipeline. Fields are read-only within an evaluator; any mutations to taint,
// counters, or termination status are applied by the pipeline orchestrator
// after the decision is emitted, never inside an evaluator.
type Session struct {
	// ID is the unique session identifier from the X-TrueBearing-Session-ID
	// request header.
	ID string

	// AgentName is the registered name of the agent that owns this session,
	// extracted from the JWT's "agent" claim at auth time.
	AgentName string

	// PolicyFingerprint is the SHA-256 fingerprint of the policy that was
	// active when this session was first created. The proxy rejects tool calls
	// if the currently loaded policy fingerprint differs from this value.
	PolicyFingerprint string

	// Tainted is true if any tool with taint.applies = true has been called
	// in this session and no subsequent tool with taint.clears = true has run.
	Tainted bool

	// ToolCallCount is the number of tool calls recorded for this session,
	// including both allowed and denied calls.
	ToolCallCount int

	// EstimatedCostUSD is the accumulated estimated cost of all tool calls
	// in this session, in US dollars.
	EstimatedCostUSD float64

	// Terminated is true if this session was hard-terminated by an operator
	// via the session terminate command. Terminated sessions reject all
	// subsequent tool calls with 410 Gone.
	Terminated bool
}

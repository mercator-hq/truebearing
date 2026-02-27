// Package session owns in-memory session state loading and the types passed to the evaluation pipeline.
//
// It does not own persistence (see package store) or evaluation decisions (see package engine).
//
// Invariant: session state presented to evaluators is a snapshot; mutations happen in the
// pipeline orchestrator after a decision is emitted, never inside an evaluator.
package session

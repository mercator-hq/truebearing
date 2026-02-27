// Package budget owns cost accumulation types and helpers used by the budget evaluator.
//
// It does not own session persistence (see package store) or budget enforcement decisions
// (see package engine).
//
// Invariant: budget accounting is read-only during evaluation; counter increments happen
// in the pipeline orchestrator after a decision is emitted, never inside an evaluator.
package budget

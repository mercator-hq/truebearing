// Package engine implements the TrueBearing evaluation pipeline.
//
// It is the only package that makes allow/deny/escalate decisions.
// It does not own session persistence (see package store) or audit signing (see package audit).
//
// Invariants:
//  1. Every call to Pipeline.Evaluate must result in exactly one AuditRecord being written,
//     regardless of decision outcome. Callers must not write audit records themselves.
//  2. Evaluators are pure: they read from session and policy, never write to either.
//  3. First failure terminates the pipeline; no subsequent evaluators run.
//  4. An evaluator that returns a non-nil error produces a Deny decision. Errors are never
//     propagated to callers — they are converted to decisions.
//  5. Shadow mode is applied at the pipeline level. Evaluators always return Deny or Escalate
//     when a violation is found; the pipeline converts to ShadowDeny based on enforcement mode.
package engine

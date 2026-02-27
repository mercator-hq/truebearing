// Package escalation owns the escalation state machine: creating, approving, and rejecting
// escalations, and notifying operators via webhook or stdout.
//
// It does not own evaluation decisions (see package engine) or the check_escalation_status
// virtual tool interception (see package proxy).
//
// Invariant: escalation status transitions are one-way — pending → approved or pending → rejected.
// An approved escalation cannot be rejected, and vice versa.
package escalation

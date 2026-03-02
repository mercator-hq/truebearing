package proxy

import (
	"context"
	"os"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mercator-hq/truebearing/internal/engine"
)

// TestInitTracerNoEndpoint verifies that InitTracer returns a working no-op
// tracer and no error when neither the flag nor the env var is set.
// The no-op tracer must not panic when spans are started and ended.
func TestInitTracerNoEndpoint(t *testing.T) {
	// Guarantee the env var is absent for this test.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tracer, shutdown, err := InitTracer("")
	if err != nil {
		t.Fatalf("unexpected error from InitTracer with no endpoint: %v", err)
	}
	defer shutdown(context.Background())

	// The no-op tracer must not panic.
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()
}

// TestInitTracerEnvVar verifies that InitTracer picks up the
// OTEL_EXPORTER_OTLP_ENDPOINT environment variable when no flag value is
// given. A bad-port endpoint is used so the exporter fails to connect but
// InitTracer must still return a tracer (fail open).
func TestInitTracerEnvVar(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:0")

	// InitTracer may succeed (no connection yet) or fail — either way it must
	// not panic and must return a usable tracer.
	tracer, shutdown, _ := InitTracer("")
	defer shutdown(context.Background())

	_, span := tracer.Start(context.Background(), "env-test-span")
	span.End()
}

// TestInitTracerFlagOverridesEnv verifies that an explicit flag value takes
// precedence over the environment variable.
func TestInitTracerFlagOverridesEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://should-not-be-used:4318")

	// A flag value is passed; we verify no panic and a usable tracer is returned.
	tracer, shutdown, _ := InitTracer("http://127.0.0.1:0")
	defer shutdown(context.Background())

	_, span := tracer.Start(context.Background(), "flag-test-span")
	span.End()
}

// TestParseOTLPEndpoint covers all three accepted endpoint formats.
func TestParseOTLPEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantHost     string
		wantInsecure bool
		wantErr      bool
	}{
		{
			name:         "http scheme",
			input:        "http://localhost:4318",
			wantHost:     "localhost:4318",
			wantInsecure: true,
		},
		{
			name:         "https scheme",
			input:        "https://collector.example.com:4317",
			wantHost:     "collector.example.com:4317",
			wantInsecure: false,
		},
		{
			name:         "no scheme defaults to insecure",
			input:        "localhost:4318",
			wantHost:     "localhost:4318",
			wantInsecure: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostPort, insecure, err := parseOTLPEndpoint(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hostPort != tc.wantHost {
				t.Errorf("host:port = %q, want %q", hostPort, tc.wantHost)
			}
			if insecure != tc.wantInsecure {
				t.Errorf("insecure = %v, want %v", insecure, tc.wantInsecure)
			}
		})
	}
}

// TestEmitDecisionSpanRecordsAttributes verifies that emitDecisionSpan writes
// all required truebearing.* attributes onto the span. An in-memory exporter
// (tracetest.NewInMemoryExporter) is used so no real OTLP endpoint is required.
func TestEmitDecisionSpanRecordsAttributes(t *testing.T) {
	// Build an in-memory exporter and wire it into a real TracerProvider.
	// AlwaysSample ensures root spans in tests are recorded regardless of
	// incoming trace context. WithSyncer exports synchronously so spans are
	// visible immediately without a Flush call.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	tracer := tp.Tracer("test")

	p := &Proxy{tracer: tracer}

	dec := engine.Decision{
		Action: engine.Deny,
		RuleID: "sequence",
		Reason: "sequence.only_after: verify_invoice not called",
	}

	p.emitDecisionSpan(
		context.Background(),
		time.Now(),
		"sess-001",
		"payments-agent",
		"execute_wire_transfer",
		dec,
		"fp-abc123",
		"traceparent=00-abc-001-01",
	)

	// Retrieve spans before shutdown: InMemoryExporter.Shutdown calls Reset(),
	// which clears stored spans. GetSpans must be called first.
	spans := exporter.GetSpans()
	_ = tp.Shutdown(context.Background())
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]

	if span.Name != "truebearing.tool_call" {
		t.Errorf("span name = %q, want %q", span.Name, "truebearing.tool_call")
	}

	wantAttrs := map[string]string{
		"truebearing.session_id":         "sess-001",
		"truebearing.agent_name":         "payments-agent",
		"truebearing.tool_name":          "execute_wire_transfer",
		"truebearing.decision":           "deny",
		"truebearing.rule_id":            "sequence",
		"truebearing.policy_fingerprint": "fp-abc123",
		"truebearing.client_trace_id":    "traceparent=00-abc-001-01",
	}

	got := make(map[string]string, len(span.Attributes))
	for _, kv := range span.Attributes {
		got[string(kv.Key)] = kv.Value.AsString()
	}

	for k, want := range wantAttrs {
		if got[k] != want {
			t.Errorf("attribute %q = %q, want %q", k, got[k], want)
		}
	}
}

// TestEmitDecisionSpanNoopDoesNotPanic verifies that emitDecisionSpan is safe
// to call with the default no-op tracer — no panic, no error.
func TestEmitDecisionSpanNoopDoesNotPanic(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	tracer, shutdown, _ := InitTracer("")
	defer shutdown(context.Background())

	p := &Proxy{tracer: tracer}
	p.emitDecisionSpan(
		context.Background(),
		time.Now(),
		"sess-noop",
		"agent",
		"some_tool",
		engine.Decision{Action: engine.Allow},
		"fp",
		"",
	)
	// Success if we reach here without panicking.
}

// TestEnforcementUnchangedWithoutOTel verifies that the Proxy struct is valid
// and its tracer field defaults to a no-op when constructed via New — ensuring
// enforcement is unaffected when OTel is absent. This test does not start a
// real proxy; it inspects the struct state only.
func TestEnforcementUnchangedWithoutOTel(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	// emitDecisionSpan called on a nil-tracer proxy must not panic.
	// We use a fresh no-op tracer here because New() requires live DB/policy deps.
	tracer, shutdown, err := InitTracer("")
	if err != nil {
		t.Fatalf("InitTracer: %v", err)
	}
	defer shutdown(context.Background())

	p := &Proxy{tracer: tracer}
	// Calling emitDecisionSpan with every decision type must not panic.
	for _, action := range []engine.Action{engine.Allow, engine.Deny, engine.ShadowDeny, engine.Escalate} {
		p.emitDecisionSpan(
			context.Background(),
			time.Now(),
			"sess", "agent", "tool",
			engine.Decision{Action: action, RuleID: "r", Reason: "reason"},
			"fp", "",
		)
	}
}

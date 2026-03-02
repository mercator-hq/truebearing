package proxy

// Package proxy — otel.go
//
// OTel tracer setup for the TrueBearing proxy. Initialising and emitting spans
// is a proxy concern: it depends on the HTTP request context and the full
// pipeline Decision struct. No OTel code appears in internal/engine/ or any
// other internal package.
//
// Design: fail open on observability. If OTel is unconfigured or the exporter
// fails to initialise, a no-op tracer is returned and the proxy operates
// identically to a non-OTel deployment. A tracer failure must never cause a
// tool call to be blocked.

import (
	"context"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// InitTracer sets up the OTel TracerProvider and returns a tracer for use by
// the proxy. endpoint is sourced from the --otel-endpoint CLI flag; if empty,
// OTEL_EXPORTER_OTLP_ENDPOINT is checked. If neither is set, a no-op tracer is
// returned and no OTel infrastructure is initialised.
//
// The returned shutdown function flushes pending spans and must be called on
// proxy shutdown. It is safe to call even when the no-op tracer is active.
//
// Justification for OTel dependency: the standard library has no distributed
// tracing support. go.opentelemetry.io/otel is the CNCF-graduated, vendor-
// neutral tracing API; it is the only package that can emit spans to Jaeger,
// Datadog, Grafana, and any other OTLP-compatible backend simultaneously.
func InitTracer(endpoint string) (trace.Tracer, func(context.Context), error) {
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		// No endpoint configured: return a no-op tracer. Observability is
		// advisory — the proxy must function identically without it.
		return noop.NewTracerProvider().Tracer("truebearing"), func(context.Context) {}, nil
	}

	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "truebearing"
	}

	hostPort, insecure, err := parseOTLPEndpoint(endpoint)
	if err != nil {
		// Malformed endpoint: fail open with a no-op tracer rather than
		// crashing the proxy. The error is returned so the caller can log it.
		return noop.NewTracerProvider().Tracer("truebearing"), func(context.Context) {}, err
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(hostPort),
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exp, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		// Exporter construction failed: fail open. The error is returned so
		// cmd/serve.go can log a warning, but the proxy does not exit.
		return noop.NewTracerProvider().Tracer("truebearing"), func(context.Context) {}, err
	}

	res, _ := sdkresource.New(
		context.Background(),
		sdkresource.WithAttributes(semconv.ServiceName(serviceName)),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	shutdown := func(ctx context.Context) {
		// Ignore shutdown errors — the proxy is already exiting.
		_ = tp.Shutdown(ctx)
	}

	return tp.Tracer("truebearing"), shutdown, nil
}

// parseOTLPEndpoint extracts the host:port pair and whether to use an insecure
// connection from a raw endpoint string. Accepted formats:
//
//	http://host:port   → host:port, insecure=true
//	https://host:port  → host:port, insecure=false
//	host:port          → host:port, insecure=true  (local dev default)
func parseOTLPEndpoint(raw string) (hostPort string, insecure bool, err error) {
	if !strings.Contains(raw, "://") {
		// No scheme: treat as host:port, default to insecure for local use.
		return raw, true, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	insecure = u.Scheme == "http"
	return u.Host, insecure, nil
}

package proxy

import (
	"fmt"
	"net/http"
)

// tracingHeaders lists the distributed tracing headers the proxy recognises,
// in priority order (first match wins). The order matches the prevalence of
// each tracing system among design-partner stacks.
var tracingHeaders = []string{
	"traceparent",            // W3C standard — LangSmith, Jaeger, Honeycomb, most modern stacks
	"x-datadog-trace-id",     // Datadog
	"x-cloud-trace-context",  // Google Cloud Trace
	"x-amzn-trace-id",        // AWS X-Ray
	"x-b3-traceid",           // Zipkin B3 (LangChain default in some configs)
}

// ExtractClientTraceID reads the first recognised distributed tracing header
// from the incoming request and returns it as "<header-name>=<value>".
//
// Preserving the header name in the return value allows audit query consumers
// to distinguish between, for example, a Datadog trace ID and a W3C traceparent
// that happen to share the same numeric value.
//
// Returns "" if no recognised tracing header is present. The empty string is
// stored as a NULL in audit_log.client_trace_id via the omitempty JSON tag on
// AuditRecord.ClientTraceID, so absent headers produce no noise in query output.
//
// This function is pure: it has no side effects and does not log.
func ExtractClientTraceID(headers http.Header) string {
	for _, h := range tracingHeaders {
		if v := headers.Get(h); v != "" {
			return fmt.Sprintf("%s=%s", h, v)
		}
	}
	return ""
}

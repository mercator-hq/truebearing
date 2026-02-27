package proxy_test

import (
	"net/http"
	"testing"

	"github.com/mercator-hq/truebearing/internal/proxy"
)

func TestExtractClientTraceID(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "no tracing headers",
			headers: map[string]string{},
			want:    "",
		},
		{
			name:    "traceparent only",
			headers: map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			want:    "traceparent=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
		{
			name:    "x-datadog-trace-id only",
			headers: map[string]string{"x-datadog-trace-id": "1234567890"},
			want:    "x-datadog-trace-id=1234567890",
		},
		{
			name:    "x-cloud-trace-context only",
			headers: map[string]string{"x-cloud-trace-context": "105445aa7843bc8bf206b120001000/1;o=1"},
			want:    "x-cloud-trace-context=105445aa7843bc8bf206b120001000/1;o=1",
		},
		{
			name:    "x-amzn-trace-id only",
			headers: map[string]string{"x-amzn-trace-id": "Root=1-5e0c223a-e270cdddad21be09e8c61a16"},
			want:    "x-amzn-trace-id=Root=1-5e0c223a-e270cdddad21be09e8c61a16",
		},
		{
			name:    "x-b3-traceid only",
			headers: map[string]string{"x-b3-traceid": "a0d5f7b1c3e2d4a6"},
			want:    "x-b3-traceid=a0d5f7b1c3e2d4a6",
		},
		{
			// Design: when multiple headers are present, the highest-priority header wins.
			// Priority order matches the field order of tracingHeaders.
			name: "traceparent and x-datadog-trace-id both present — traceparent wins",
			headers: map[string]string{
				"traceparent":        "00-trace-span-01",
				"x-datadog-trace-id": "9999",
			},
			want: "traceparent=00-trace-span-01",
		},
		{
			name: "x-datadog-trace-id and x-b3-traceid both present — datadog wins",
			headers: map[string]string{
				"x-datadog-trace-id": "42",
				"x-b3-traceid":       "deadbeef",
			},
			want: "x-datadog-trace-id=42",
		},
		{
			name: "x-amzn-trace-id and x-b3-traceid both present — amzn wins",
			headers: map[string]string{
				"x-amzn-trace-id": "Root=1-abc",
				"x-b3-traceid":    "cafebabe",
			},
			want: "x-amzn-trace-id=Root=1-abc",
		},
		{
			name: "all five headers present — traceparent wins",
			headers: map[string]string{
				"traceparent":           "00-aaa-bbb-01",
				"x-datadog-trace-id":    "111",
				"x-cloud-trace-context": "222",
				"x-amzn-trace-id":       "Root=333",
				"x-b3-traceid":          "444",
			},
			want: "traceparent=00-aaa-bbb-01",
		},
		{
			name: "unrecognised header present, no standard header",
			headers: map[string]string{
				"x-custom-trace": "should-be-ignored",
			},
			want: "",
		},
		{
			name: "header present with empty value — treated as absent",
			headers: map[string]string{
				"traceparent": "",
			},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := make(http.Header)
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			got := proxy.ExtractClientTraceID(h)
			if got != tc.want {
				t.Errorf("ExtractClientTraceID() = %q; want %q", got, tc.want)
			}
		})
	}
}

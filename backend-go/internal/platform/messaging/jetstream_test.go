package messaging

import (
	"testing"

	"github.com/nats-io/nats.go"
)

func TestSubjectSuffix(t *testing.T) {
	tests := map[string]string{
		"note.created":        "note.created",
		" Comment Liked ":     "comment_liked",
		"note/unsafe.created": "note_unsafe.created",
		"...":                 "unknown",
	}
	for input, want := range tests {
		if got := subjectSuffix(input); got != want {
			t.Fatalf("subjectSuffix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNATSHeaderCarrierIsCaseInsensitive(t *testing.T) {
	carrier := NATSHeaderCarrier(nats.Header{"traceparent": {"00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"}})
	if got := carrier.Get("Traceparent"); got == "" {
		t.Fatal("carrier did not read a differently cased traceparent")
	}
	carrier.Set("tracestate", "vendor=value")
	if got := carrier.Get("TRACESTATE"); got != "vendor=value" {
		t.Fatalf("tracestate = %q", got)
	}
}

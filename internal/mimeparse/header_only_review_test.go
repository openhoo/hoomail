package mimeparse

import (
	"bytes"
	"testing"
)

func TestHeaderOnlyReview(t *testing.T) {
	raw := []byte("From: a@example.test\r\nDate: Wed, 25 Nov 2026 12:00:00 +0000\r\n")
	for _, limits := range []Limits{{}, InspectionLimits} {
		doc, err := Parse(raw, limits)
		if err != nil || doc.SemanticError != nil || doc.Root == nil || len(doc.Root.DecodedBody) != 0 || !bytes.Equal(raw, doc.Raw) {
			t.Fatalf("missing header-only message with limits %+v: root=%v error=%v/%v", limits, doc.Root, err, doc.SemanticError)
		}
		if limits != (Limits{}) && (doc.Root.RawBody.Start != len(raw) || doc.Root.RawBody.End != len(raw)) {
			t.Errorf("synthetic separator in raw body offsets: %+v", doc.Root.RawBody)
		}
	}
	for _, raw := range [][]byte{[]byte("From: a@example.test"), []byte("From: a@example.test\r\nnot a header\r\n")} {
		doc, err := Parse(raw, InspectionLimits)
		if err != nil || doc.SemanticError == nil {
			t.Errorf("invalid header termination read as complete: %v/%v", err, doc.SemanticError)
		}
	}
	limits := InspectionLimits
	limits.MaxPhysicalLines = 1
	doc, err := Parse(raw, limits)
	if err != nil || doc.Root != nil || !doc.Truncated {
		t.Errorf("truncated header accepted: %+v/%v", doc, err)
	}
}

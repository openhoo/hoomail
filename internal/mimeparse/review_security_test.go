package mimeparse

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseReviewEncodedMessageHonorsInspectionLimits(t *testing.T) {
	inner := "Content-Type: multipart/mixed; boundary=i\r\n\r\n" + strings.Repeat("--i\r\nContent-Type: text/plain\r\n\r\nbody\r\n", 8) + "--i--\r\n"
	raw := []byte("Content-Type: message/rfc822\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(inner)))
	for _, test := range []struct {
		cause  string
		limits Limits
	}{
		{"MIME parts", Limits{MaxParts: 4}},
		{"MIME depth", Limits{MaxDepth: 2}},
		{"header fields", Limits{MaxHeaderFields: 3}},
		{"header bytes", Limits{MaxHeaderBytes: 100}},
		{"physical lines", Limits{MaxPhysicalLines: 8}},
	} {
		t.Run(test.cause, func(t *testing.T) {
			doc, err := Parse(raw, test.limits)
			if err != nil {
				t.Fatal(err)
			}
			if !doc.Truncated || !strings.Contains(strings.Join(doc.TruncationCauses, ","), test.cause) {
				t.Fatalf("encoded message bypassed cap: truncated=%v causes=%v", doc.Truncated, doc.TruncationCauses)
			}
			if doc.Presentation.Supported {
				t.Fatal("capped decoded message exposed a semantic presentation")
			}
		})
	}
}

func TestParseReviewEncodedMessageSiblingsSharePartBudget(t *testing.T) {
	inner := base64.StdEncoding.EncodeToString([]byte("Subject: inner\r\n\r\nbody"))
	part := "--b\r\nContent-Type: message/rfc822\r\nContent-Transfer-Encoding: base64\r\n\r\n" + inner + "\r\n"
	raw := []byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n" + part + part + "--b--\r\n")
	doc, err := Parse(raw, Limits{MaxParts: 4})
	if err != nil || !doc.Truncated {
		t.Fatalf("sibling decoded messages did not share part budget: error=%v truncated=%v causes=%v", err, doc.Truncated, doc.TruncationCauses)
	}
}

func TestParseReviewEmptyMultipartPartHasNoInvariantFailure(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\n\r\n--b\r\n\r\n\r\n--b--\r\n")
	if _, err := Parse(raw, InspectionLimits); err != nil {
		t.Fatalf("sender input returned invariant error: %v", err)
	}
}

func FuzzParseInspectionSenderInput(f *testing.F) {
	f.Add([]byte("From: sender@example.test\r\nContent-Type: text/plain\r\n\r\nbody"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n--b--\r\n"))
	f.Add([]byte("Content-Type: message/rfc822\r\nContent-Transfer-Encoding: base64\r\n\r\nU3ViamVjdDogaW5uZXINCg0KYm9keQ=="))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			t.Skip()
		}
		if _, err := Parse(raw, InspectionLimits); err != nil {
			t.Fatalf("sender-controlled bytes caused invariant failure: %v", err)
		}
	})
}

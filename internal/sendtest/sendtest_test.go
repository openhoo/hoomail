package sendtest

import (
	"strings"
	"testing"
	"time"

	"github.com/openhoo/hoomail/internal/httpserver"
)

func TestPlainMessageContainsMultipartAttachment(t *testing.T) {
	raw := string(buildMessage(httpserver.SendTestRequest{To: "test@example.com", Kind: "plain"}, time.Unix(1, 0).UTC()))
	for _, expected := range []string{"Subject: hoomail delivery test", "multipart/mixed", "hoot.txt", "multipart/alternative"} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("message missing %q", expected)
		}
	}
}

func TestCalendarSequenceAndUIDRemainStable(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	invite := string(buildMessage(httpserver.SendTestRequest{To: "test@example.com", Subject: "Standup", Kind: "invite"}, now))
	cancel := string(buildMessage(httpserver.SendTestRequest{To: "test@example.com", Subject: "Standup", Kind: "cancellation"}, now))
	for _, expected := range []string{"METHOD:REQUEST", "SEQUENCE:0", "UID:hoomail-demo-test-example-com@hoomail.local"} {
		if !strings.Contains(invite, expected) {
			t.Fatalf("invite missing %q", expected)
		}
	}
	for _, expected := range []string{"METHOD:CANCEL", "SEQUENCE:2", "STATUS:CANCELLED", "UID:hoomail-demo-test-example-com@hoomail.local"} {
		if !strings.Contains(cancel, expected) {
			t.Fatalf("cancellation missing %q", expected)
		}
	}
}

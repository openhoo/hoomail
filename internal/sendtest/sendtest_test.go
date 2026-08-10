package sendtest

import (
	"context"
	"errors"
	"net"
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

func TestMessageEncodesUntrustedFields(t *testing.T) {
	raw := string(buildMessage(httpserver.SendTestRequest{
		To:      "owl+<nest>@example.com",
		Subject: "Status\r\nBcc: hidden@example.com",
		Kind:    "plain",
	}, time.Unix(1, 0).UTC()))

	if strings.Contains(raw, "\r\nBcc:") {
		t.Fatal("subject injected an additional header")
	}
	if !strings.Contains(raw, `To: <"owl+<nest>"@example.com>`) {
		t.Fatalf("recipient header was not encoded: %q", raw)
	}
	if !strings.Contains(raw, "Subject: =?UTF-8?q?Status=0D=0ABcc:_hidden@example.com?=") {
		t.Fatalf("subject header was not encoded: %q", raw)
	}
	if strings.Contains(raw, "\r\nBcc: hidden@example.com") {
		t.Fatal("plain-text recipient introduced MIME structure")
	}
	if !strings.Contains(sampleHTML("owl+<nest>@example.com"), "owl+&lt;nest&gt;@example.com") {
		t.Fatal("HTML recipient was not escaped")
	}
}

func TestSendTestCancelsStalledSMTPGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		defer connection.Close()
		buffer := make([]byte, 1)
		_, readErr := connection.Read(buffer)
		accepted <- readErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = (Sender{Address: listener.Addr().String()}).SendTest(ctx, httpserver.SendTestRequest{To: "test@example.com"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendTest error = %v, want context deadline exceeded", err)
	}
	select {
	case readErr := <-accepted:
		if readErr == nil {
			t.Fatal("stalled SMTP connection remained readable after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled SMTP connection was not closed after cancellation")
	}
}

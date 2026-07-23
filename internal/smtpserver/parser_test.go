package smtpserver

import (
	"strings"
	"testing"
)

func TestParseMIMEHTMLTextCIDAttachmentsAndCalendarDeduplication(t *testing.T) {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"METHOD:REQUEST",
		"BEGIN:VEVENT",
		"UID:meeting-1",
		"SEQUENCE:2",
		"SUMMARY:Planning",
		"DTSTART:20260724T100000Z",
		"DTEND:20260724T110000Z",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")
	raw := strings.Join([]string{
		"From: =?UTF-8?Q?M=C3=BCller?= <sender@example.test>",
		"To: Recipient Name <recipient@example.test>",
		"Cc: Copy <copy@example.test>",
		"Subject: =?UTF-8?Q?MIME_=E2=9C=93?=",
		"X-Test: first",
		"X-Test: second",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"--outer",
		"Content-Type: multipart/alternative; boundary=alt",
		"",
		"--alt",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"plain body",
		"--alt",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>html body<img src=\"cid:logo@example.test\"></p>",
		"--alt--",
		"--outer",
		"Content-Type: image/png; name=logo.png",
		"Content-Disposition: inline; filename=logo.png",
		"Content-ID: <logo@example.test>",
		"Content-Transfer-Encoding: base64",
		"",
		"aW1hZ2U=",
		"--outer",
		"Content-Type: text/calendar; method=REQUEST; charset=utf-8",
		"Content-Disposition: attachment; filename=invite.ics",
		"",
		ics,
		"--outer",
		"Content-Type: application/ics; name=copy.ics",
		"Content-Disposition: attachment; filename=copy.ics",
		"",
		ics,
		"--outer--",
		"",
	}, "\r\n")

	input, err := Parse([]byte(raw), "fallback@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if input.Subject == nil || *input.Subject != "MIME ✓" {
		t.Fatalf("subject = %v", input.Subject)
	}
	if input.FromName == nil || *input.FromName != "Müller" {
		t.Fatalf("from name = %v", input.FromName)
	}
	if input.Text == nil || !strings.Contains(*input.Text, "plain body") {
		t.Fatalf("text = %v", input.Text)
	}
	if input.HTML == nil || !strings.Contains(*input.HTML, "cid:logo@example.test") {
		t.Fatalf("html = %v", input.HTML)
	}
	if len(input.Attachments) != 3 {
		t.Fatalf("attachments = %d, want 3", len(input.Attachments))
	}
	image := input.Attachments[0]
	if image.Filename == nil || *image.Filename != "logo.png" || image.ContentID == nil || *image.ContentID != "logo@example.test" || string(image.Content) != "image" {
		t.Fatalf("unexpected CID attachment: %#v", image)
	}
	if len(input.ICalEvents) != 1 {
		t.Fatalf("calendar events = %d, want deduplicated 1", len(input.ICalEvents))
	}
	if event := input.ICalEvents[0]; event.UID != "meeting-1" || event.Sequence != 2 || event.Method != "REQUEST" {
		t.Fatalf("unexpected calendar event: %#v", event)
	}
	if got := input.Headers["x-test"]; got != "X-Test: second" {
		t.Fatalf("duplicate header result = %q", got)
	}
	if got, want := strings.Join(input.Recipients, ","), "recipient@example.test,copy@example.test"; got != want {
		t.Fatalf("fallback recipients = %q, want %q", got, want)
	}
	if string(input.Raw) != raw {
		t.Fatal("raw MIME was not retained exactly")
	}
}

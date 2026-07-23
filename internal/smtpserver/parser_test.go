package smtpserver

import (
	"strings"
	"testing"

	"github.com/openhoo/hoomail/internal/store"
)

func TestParseMIMESelection(t *testing.T) {
	tests := []struct {
		name  string
		raw   []byte
		check func(*testing.T, parsedMessage)
	}{
		{
			name: "ordinary alternative selects html with plain fallback",
			raw: mimeFixture(
				"Content-Type: multipart/alternative; boundary=alt",
				"--alt\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain fallback\r\n"+
					"--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html last</p>\r\n--alt--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "plain fallback", "<p>html last</p>")
			},
		},
		{
			name: "reversed alternative order selects last supported",
			raw: mimeFixture(
				"Content-Type: multipart/alternative; boundary=alt",
				"--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html first</p>\r\n"+
					"--alt\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain last\r\n--alt--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "plain last", "")
			},
		},
		{
			name: "nested alternative related uses non-first start root",
			raw: mimeFixture(
				"Content-Type: multipart/alternative; boundary=alt",
				"--alt\r\nContent-Type: text/plain\r\n\r\nfallback\r\n"+
					"--alt\r\nContent-Type: multipart/related; boundary=rel; start=\"<root@example.test>\"\r\n\r\n"+
					"--rel\r\nContent-Type: image/png; name=logo.png\r\nContent-ID: <logo@example.test>\r\nContent-Transfer-Encoding: base64\r\n\r\naW1hZ2U=\r\n"+
					"--rel\r\nContent-Type: text/html; charset=utf-8\r\nContent-ID: <root@example.test>\r\n\r\n<p>chosen<img src=\"cid:logo@example.test\"></p>\r\n"+
					"--rel--\r\n--alt--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "fallback", "<p>chosen<img src=\"cid:logo@example.test\"></p>")
				assertAttachmentIDs(t, got, "logo@example.test")
			},
		},
		{
			name: "mixed attachment CID is not related resource",
			raw: mimeFixture(
				"Content-Type: multipart/mixed; boundary=mixed",
				"--mixed\r\nContent-Type: multipart/related; boundary=rel\r\n\r\n"+
					"--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p><img src=\"cid:related@example.test\"><img src=\"cid:outside@example.test\"></p>\r\n"+
					"--rel\r\nContent-Type: image/png; name=related.png\r\nContent-ID: <related@example.test>\r\n\r\nrelated\r\n--rel--\r\n"+
					"--mixed\r\nContent-Type: image/png; name=outside.png\r\nContent-ID: <outside@example.test>\r\n\r\noutside\r\n--mixed--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "", "<p><img src=\"cid:related@example.test\"><img src=\"cid:outside@example.test\"></p>")
				assertAttachmentIDs(t, got, "related@example.test", "")
				if string(got.attachments[0].content) != "related" || string(got.attachments[1].content) != "outside" {
					t.Fatalf("attachments = %#v", got.attachments)
				}
			},
		},
		{
			name: "mixed keeps first body and later text leaf as attachment",
			raw: mimeFixture(
				"Content-Type: multipart/mixed; boundary=mixed",
				"--mixed\r\nContent-Type: text/plain\r\n\r\nfirst body\r\n"+
					"--mixed\r\nContent-Type: text/html\r\n\r\n<p>second body</p>\r\n--mixed--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "first body", "")
				if len(got.attachments) != 1 || string(got.attachments[0].content) != "<p>second body</p>" {
					t.Fatalf("attachments = %#v", got.attachments)
				}
			},
		},
		{
			name:  "windows 1252 decodes to utf8",
			raw:   append(mimeFixture("Content-Type: text/plain; charset=windows-1252", "price "), 0x80),
			check: func(t *testing.T, got parsedMessage) { assertBody(t, got, "price €", "") },
		},
		{
			name:  "iso 8859 1 decodes to utf8",
			raw:   append(mimeFixture("Content-Type: text/plain; charset=iso-8859-1", "caf"), 0xe9),
			check: func(t *testing.T, got parsedMessage) { assertBody(t, got, "café", "") },
		},
		{
			name: "unknown transfer encoding is opaque",
			raw:  mimeFixture("Content-Type: text/html\r\nContent-Transfer-Encoding: x-unknown", "<p>not rendered</p>"),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "", "")
				assertOpaqueAttachment(t, got, "<p>not rendered</p>")
			},
		},
		{
			name:  "missing content type uses mime default",
			raw:   mimeFixture("", "default plain"),
			check: func(t *testing.T, got parsedMessage) { assertBody(t, got, "default plain", "") },
		},
		{
			name: "malformed content type is opaque",
			raw:  mimeFixture("Content-Type: text/html; charset", "<p>not rendered</p>"),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "", "")
				assertOpaqueAttachment(t, got, "<p>not rendered</p>")
			},
		},
		{
			name: "alternative scopes related resources to selected branch",
			raw: mimeFixture(
				"Content-Type: multipart/alternative; boundary=alt",
				"--alt\r\nContent-Type: multipart/related; boundary=old\r\n\r\n"+
					"--old\r\nContent-Type: text/html\r\n\r\n<p>old</p>\r\n--old\r\nContent-Type: image/png\r\nContent-ID: <old@example.test>\r\n\r\nold\r\n--old--\r\n"+
					"--alt\r\nContent-Type: multipart/related; boundary=new\r\n\r\n"+
					"--new\r\nContent-Type: text/html\r\n\r\n<p>new</p>\r\n--new\r\nContent-Type: image/png\r\nContent-ID: <new@example.test>\r\n\r\nnew\r\n--new--\r\n--alt--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "", "<p>new</p>")
				assertAttachmentIDs(t, got, "new@example.test")
			},
		},
		{
			name: "alternative does not import fallback branch attachments",
			raw: mimeFixture(
				"Content-Type: multipart/alternative; boundary=alt",
				"--alt\r\nContent-Type: multipart/related; boundary=old\r\n\r\n"+
					"--old\r\nContent-Type: text/plain\r\n\r\nplain fallback\r\n"+
					"--old\r\nContent-Type: image/png\r\nContent-ID: <old@example.test>\r\n\r\nold\r\n--old--\r\n"+
					"--alt\r\nContent-Type: text/html\r\n\r\n<p>selected</p>\r\n--alt--\r\n",
			),
			check: func(t *testing.T, got parsedMessage) {
				assertBody(t, got, "plain fallback", "<p>selected</p>")
				assertAttachmentIDs(t, got)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, err := Parse(test.raw, "fallback@example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, simplifyParsed(input))
		})
	}
}

func TestParseMIMEAlternativeRetainsOnlyCalendarFromUnselectedBranch(t *testing.T) {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "METHOD:REQUEST", "BEGIN:VEVENT", "UID:alternative-invite", "SEQUENCE:3",
		"SUMMARY:Alternative invite", "DTSTART:20260725T100000Z", "DTEND:20260725T110000Z", "END:VEVENT", "END:VCALENDAR",
	}, "\r\n")
	raw := strings.Join([]string{
		"From: Sender <sender@example.test>",
		"To: Recipient <recipient@example.test>",
		"Subject: Calendar alternative", "MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=alt", "", "--alt",
		"Content-Type: text/plain; charset=utf-8", "", "Selected plain body", "--alt",
		"Content-Type: multipart/mixed; boundary=calendar-branch", "", "--calendar-branch",
		"Content-Type: text/calendar; method=REQUEST; charset=utf-8; name=invite.ics", "Content-Disposition: attachment; filename=invite.ics", "", ics,
		"--calendar-branch", "Content-Type: image/png; name=unselected.png", "Content-Disposition: attachment; filename=unselected.png", "Content-Transfer-Encoding: base64", "", "aW1hZ2U=",
		"--calendar-branch--", "--alt--", "",
	}, "\r\n")

	input, err := Parse([]byte(raw), "fallback@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if input.Text == nil || strings.TrimSpace(*input.Text) != "Selected plain body" || input.HTML != nil {
		t.Fatalf("bodies: text=%v html=%v", input.Text, input.HTML)
	}
	if len(input.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want only calendar", input.Attachments)
	}
	attachment := input.Attachments[0]
	if attachment.ContentType == nil || *attachment.ContentType != "text/calendar" || attachment.Filename == nil || *attachment.Filename != "invite.ics" || string(attachment.Content) != ics {
		t.Fatalf("calendar attachment = %#v", attachment)
	}
	if len(input.ICalEvents) != 1 || input.ICalEvents[0].UID != "alternative-invite" || input.ICalEvents[0].Sequence != 3 {
		t.Fatalf("calendar events = %#v", input.ICalEvents)
	}
}

func TestParseMIMEPreservesMetadataAttachmentsAndCalendarDeduplication(t *testing.T) {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "METHOD:REQUEST", "BEGIN:VEVENT", "UID:meeting-1", "SEQUENCE:2",
		"SUMMARY:Planning", "DTSTART:20260724T100000Z", "DTEND:20260724T110000Z", "END:VEVENT", "END:VCALENDAR",
	}, "\r\n")
	raw := strings.Join([]string{
		"From: =?UTF-8?Q?M=C3=BCller?= <sender@example.test>",
		"To: Recipient Name <recipient@example.test>",
		"Cc: Copy <copy@example.test>",
		"Subject: =?UTF-8?Q?MIME_=E2=9C=93?=", "X-Test: first", "X-Test: second", "MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer", "", "--outer",
		"Content-Type: multipart/alternative; boundary=alt", "", "--alt", "Content-Type: text/plain", "", "plain body",
		"--alt", "Content-Type: text/html", "", "<p>html body<img src=\"cid:logo@example.test\"></p>", "--alt--", "--outer",
		"Content-Type: image/png; name=logo.png", "Content-Disposition: inline; filename=logo.png", "Content-ID: <logo@example.test>",
		"Content-Transfer-Encoding: base64", "", "aW1hZ2U=", "--outer",
		"Content-Type: text/calendar; method=REQUEST; charset=utf-8", "Content-Disposition: attachment; filename=invite.ics", "", ics, "--outer",
		"Content-Type: application/ics; name=copy.ics", "Content-Disposition: attachment; filename=copy.ics", "", ics, "--outer--", "",
	}, "\r\n")

	input, err := Parse([]byte(raw), "fallback@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if input.Subject == nil || *input.Subject != "MIME ✓" || input.FromName == nil || *input.FromName != "Müller" {
		t.Fatalf("metadata = %#v", input)
	}
	if input.Text == nil || *input.Text != "plain body" || input.HTML == nil || !strings.Contains(*input.HTML, "html body") {
		t.Fatalf("bodies: text=%v html=%v", input.Text, input.HTML)
	}
	if len(input.Attachments) != 3 {
		t.Fatalf("attachments = %d, want 3", len(input.Attachments))
	}
	if image := input.Attachments[0]; image.Filename == nil || *image.Filename != "logo.png" || image.ContentID != nil || string(image.Content) != "image" {
		t.Fatalf("mixed attachment = %#v", image)
	}
	if len(input.ICalEvents) != 1 || input.ICalEvents[0].UID != "meeting-1" || input.ICalEvents[0].Sequence != 2 {
		t.Fatalf("calendar events = %#v", input.ICalEvents)
	}
	if input.Headers["x-test"] != "X-Test: second" {
		t.Fatalf("duplicate header = %q", input.Headers["x-test"])
	}
	if got := strings.Join(input.Recipients, ","); got != "recipient@example.test,copy@example.test" {
		t.Fatalf("recipients = %q", got)
	}
	if string(input.Raw) != raw {
		t.Fatal("raw MIME was not retained exactly")
	}
}

type parsedAttachment struct {
	contentType, contentID string
	content                []byte
}
type parsedMessage struct {
	text, html  string
	attachments []parsedAttachment
}

func simplifyParsed(input store.StoreMessageInput) parsedMessage {
	got := parsedMessage{}
	if input.Text != nil {
		got.text = strings.TrimSpace(*input.Text)
	}
	if input.HTML != nil {
		got.html = strings.TrimSpace(*input.HTML)
	}
	for _, attachment := range input.Attachments {
		item := parsedAttachment{content: attachment.Content}
		if attachment.ContentType != nil {
			item.contentType = *attachment.ContentType
		}
		if attachment.ContentID != nil {
			item.contentID = *attachment.ContentID
		}
		got.attachments = append(got.attachments, item)
	}
	return got
}

func mimeFixture(headers, body string) []byte {
	lines := "From: Sender <sender@example.test>\r\nTo: Recipient <recipient@example.test>\r\nSubject: fixture\r\n"
	if headers != "" {
		lines += headers + "\r\n"
	}
	return []byte(lines + "\r\n" + body)
}

func assertBody(t *testing.T, got parsedMessage, text, html string) {
	t.Helper()
	if got.text != text || got.html != html {
		t.Fatalf("body = text %q html %q, want %q %q", got.text, got.html, text, html)
	}
}

func assertAttachmentIDs(t *testing.T, got parsedMessage, want ...string) {
	t.Helper()
	if len(got.attachments) != len(want) {
		t.Fatalf("attachments = %#v, want IDs %v", got.attachments, want)
	}
	for index := range want {
		if got.attachments[index].contentID != want[index] {
			t.Fatalf("attachment %d ID = %q, want %q", index, got.attachments[index].contentID, want[index])
		}
	}
}

func assertOpaqueAttachment(t *testing.T, got parsedMessage, content string) {
	t.Helper()
	if len(got.attachments) != 1 || got.attachments[0].contentType != "application/octet-stream" || string(got.attachments[0].content) != content {
		t.Fatalf("opaque attachment = %#v", got.attachments)
	}
}

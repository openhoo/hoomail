package smtpserver

import (
	"strings"
	"testing"

	"github.com/openhoo/hoomail/internal/store"
)

var benchmarkParsedMessage store.StoreMessageInput

func BenchmarkParseMIME(b *testing.B) {
	fixtures := []struct {
		name       string
		raw        []byte
		recipients []string
	}{
		{
			name: "SmallPlain",
			raw: []byte(strings.Join([]string{
				"From: Sender <sender@example.test>",
				"To: Recipient <recipient@example.test>",
				"Subject: Benchmark message",
				"Content-Type: text/plain; charset=utf-8",
				"",
				"A small plain-text message.",
			}, "\r\n")),
		},
		{
			name:       "RealisticMultipart",
			raw:        realisticMultipartMIMEBenchmarkFixture(),
			recipients: []string{"recipient@example.test", "hidden@example.test"},
		},
	}

	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.raw)))
			b.ResetTimer()
			for b.Loop() {
				input, err := Parse(fixture.raw, "sender@example.test", fixture.recipients)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkParsedMessage = input
			}
		})
	}
}

func realisticMultipartMIMEBenchmarkFixture() []byte {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"METHOD:REQUEST",
		"BEGIN:VEVENT",
		"UID:planning-2026@example.test",
		"SEQUENCE:4",
		"DTSTART;TZID=Europe/Berlin:20260724T100000",
		"DTEND;TZID=Europe/Berlin:20260724T113000",
		"SUMMARY:Quarterly planning",
		"DESCRIPTION:Review milestones\\nand assign owners",
		"LOCATION:Owl Room",
		"ORGANIZER;CN=Hoomail Owl:mailto:owl@example.test",
		"ATTENDEE;CN=Recipient;ROLE=REQ-PARTICIPANT:mailto:recipient@example.test",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")

	return []byte(strings.Join([]string{
		"From: =?UTF-8?Q?Hoomail_Owl?= <owl@example.test>",
		"To: Recipient <recipient@example.test>",
		"Cc: Project Team <team@example.test>",
		"Subject: =?UTF-8?Q?Quarterly_planning_=E2=9C=93?=",
		"Date: Thu, 23 Jul 2026 10:00:00 +0200",
		"Message-ID: <benchmark-message@example.test>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=benchmark-outer",
		"",
		"--benchmark-outer",
		"Content-Type: multipart/alternative; boundary=benchmark-alternative",
		"",
		"--benchmark-alternative",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hello team,=0D=0A=0D=0APlease review the quarterly plan and attached agenda.",
		"--benchmark-alternative",
		"Content-Type: multipart/related; boundary=benchmark-related",
		"",
		"--benchmark-related",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<html><body><h1>Quarterly planning</h1><p>Please review the plan.</p><img src=3D\"cid:logo@example.test\"></body></html>",
		"--benchmark-related",
		"Content-Type: image/png; name=logo.png",
		"Content-Disposition: inline; filename=logo.png",
		"Content-ID: <logo@example.test>",
		"Content-Transfer-Encoding: base64",
		"",
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
		"--benchmark-related--",
		"--benchmark-alternative--",
		"--benchmark-outer",
		"Content-Type: application/pdf; name=agenda.pdf",
		"Content-Disposition: attachment; filename=agenda.pdf",
		"Content-Transfer-Encoding: base64",
		"",
		"JVBERi0xLjQKMSAwIG9iago8PC9UeXBlL0NhdGFsb2c+PgplbmRvYmoKJSVFT0Y=",
		"--benchmark-outer",
		"Content-Type: text/calendar; method=REQUEST; charset=utf-8; name=invite.ics",
		"Content-Disposition: attachment; filename=invite.ics",
		"Content-Transfer-Encoding: 8bit",
		"",
		ics,
		"--benchmark-outer--",
		"",
	}, "\r\n"))
}

package inspect

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

func TestInspectionMailStandards(t *testing.T) {
	day := "Date: Fri, 24 Jul 2026 12:00:00 +0000"
	author := "From: Author <Writer@example.test>"
	top := day + "\r\n" + author
	mime := top + "\r\nMIME-Version: 1.0\r\n"
	dkim := "DKIM-Signature: v=1; a=rsa-sha256; d=example.test; s=mail; b=YWJj; bh=YWJj; h=From:List-Unsubscribe:List-Unsubscribe-Post"
	oneClick := "List-Unsubscribe: <https://example.test/leave>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click"
	for _, test := range []struct{ name, header, body, id, outcome, severity string }{
		{"empty date", author + "\r\nDate:", "", "message.date", "fail", "error"},
		{"weekday mismatch", author + "\r\nDate: Mon, 24 Jul 2026 12:00:00 +0000", "", "message.date", "fail", "error"},
		{"trailing author text", top + " trailing", "", "message.from", "fail", "error"},
		{"text after zone", author + "\r\n" + day + " extra", "", "message.date", "fail", "error"},
		{"date comment", author + "\r\n" + day + " (GMT)", "", "message.date", "pass", "none"},
		{"empty blind copy", top + "\r\nBcc: (recipients suppressed)", "", "message.address-fields.bcc", "pass", "none"},
		{"delegated sender", top + "\r\nSender: Delegate <assistant@example.test>", "", "message.sender", "pass", "none"},
		{"redundant sender", top + "\r\nSender: Writer@EXAMPLE.TEST", "", "message.sender", "pass", "warning"},
		{"empty sender group", top + "\r\nSender: Automatic System:;", "", "message.sender", "pass", "warning"},
		{"two sender addresses", top + "\r\nSender: System:;, Writer@example.test", "", "message.sender", "fail", "error"},
		{"utf8 recommendation", top + "\r\nSubject: " + strings.Repeat("é", 69), "", "message.line-length", "pass", "none"},
		{"character recommendation", top + "\r\nSubject: " + strings.Repeat("é", 70), "", "message.line-length", "pass", "warning"},
		{"octet limit", top + "\r\nSubject: " + strings.Repeat("é", 495), "", "message.line-length", "fail", "error"},
		{"bad message id", top + "\r\nMessage-ID: <a@b@c>", "", "message.message-id", "fail", "error"},
		{"bad dot atom", top + "\r\nContent-ID: <a..b@example.test>", "", "mime.content-id.1", "fail", "error"},
		{"international id literal", top + "\r\nReferences: (previous) <réf@[IPv6:2001:db8::1]> <next@example.test>", "", "message.threading.references", "pass", "none"},
		{"incomplete mime version comment", top + "\r\nMIME-Version: 1.0 (unfinished", "", "mime.version", "fail", "error"},
		{"empty encoding", mime + "Content-Transfer-Encoding:", "", "mime.transfer-encoding.1", "fail", "error"},
		{"missing media subtype", mime + "Content-Type: text", "", "mime.content-type.1", "fail", "error"},
		{"explicit ascii", mime + "Content-Type: text/plain; charset=US-ASCII\r\nContent-Transfer-Encoding: base64", "Y2Fmw6k=", "mime.charset.1", "fail", "error"},
		{"invalid utf8", mime + "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64", "/w==", "mime.charset.1", "fail", "error"},
		{"non-ASCII calendar missing MIME charset", mime + "Content-Type: text/calendar\r\nContent-Transfer-Encoding: 8bit", "BEGIN:VCALENDAR\r\nSUMMARY:Café\r\nEND:VCALENDAR", "mime.charset.1", "fail", "error"},
		{"html in-band charset", mime + "Content-Type: text/html\r\nContent-Transfer-Encoding: 8bit", "<meta charset=utf-8><p>Café</p>", "mime.charset.1", "observed", "none"},
		{"encoded global message", mime + "Content-Type: message/global\r\nContent-Transfer-Encoding: base64", base64.StdEncoding.EncodeToString([]byte("From: a@example.test\r\n\r\nCafé")), "mime.transfer-encoding.1", "pass", "none"},
		{"encoded rfc822 message", mime + "Content-Type: message/rfc822\r\nContent-Transfer-Encoding: base64", base64.StdEncoding.EncodeToString([]byte("From: a@example.test\r\n\r\nHello")), "mime.transfer-encoding.1", "fail", "error"},
		{"auth none", top + "\r\nAuthentication-Results: mx.example 1; (local) none", "", "authentication.results.1", "observed", "none"},
		{"auth reason and comments", top + "\r\nAuthentication-Results: mx.example; dkim (v) / 1 = pass (trace) reason=\"good; signature\" header . d = example.test", "", "authentication.results.1", "observed", "none"},
		{"auth no result", top + "\r\nAuthentication-Results: mx.example;", "", "authentication.results.1", "fail", "error"},
		{"bad auth version", top + "\r\nAuthentication-Results: mx.example x; spf=pass", "", "authentication.results.1", "fail", "error"},
		{"two missing separator", top + "\r\nAuthentication-Results: mx.example; spf=pass dkim=pass", "", "authentication.results.1", "fail", "error"},
		{"case-sensitive dkim tag", top + "\r\n" + strings.Replace(dkim, "v=1", "V=1", 1), "", "authentication.dkim.1", "fail", "error"},
		{"dkim invalid base64", top + "\r\n" + strings.Replace(dkim, "b=YWJj", "b=Y", 1), "", "authentication.dkim.1", "fail", "error"},
		{"invalid timestamp", top + "\r\n" + dkim + "; t=-1", "", "authentication.dkim.1", "fail", "error"},
		{"one click coverage absent", top + "\r\n" + oneClick, "", "unsubscribe.dkim-coverage", "fail", "error"},
		{"one click with http", top + "\r\n" + strings.Replace(oneClick, "<https:", "<http://example.test/old>, <https:", 1), "", "unsubscribe.one-click-pairing", "fail", "error"},
		{"declared calendar charset", mime + "Content-Type: text/calendar; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit", "SUMMARY:Café", "mime.charset.1", "pass", "none"},
		{"obsolete signing algorithm", top + "\r\n" + strings.Replace(dkim, "rsa-sha256", "rsa-sha1", 1), "", "authentication.dkim.1", "fail", "error"},
		{"version is not required first", top + "\r\n" + strings.Replace(dkim, "v=1; a=rsa-sha256", "a=rsa-sha256; v=1", 1), "", "authentication.dkim.1", "observed", "none"},
		{"internationalized signing domain", top + "\r\n" + strings.Replace(dkim, "d=example.test", "d=bücher.test", 1), "", "authentication.dkim.1", "observed", "none"},
		{"high byte in seven bit", mime + "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 7bit", "Café", "mime.transfer-encoding.1", "fail", "error"},
		{"trailing space in QP", mime + "Content-Transfer-Encoding: quoted-printable", "test \r\n", "mime.transfer-encoding.1", "fail", "error"},
		{"lowercase escapes in QP", mime + "Content-Transfer-Encoding: quoted-printable", "=2e", "mime.transfer-encoding.1", "fail", "error"},
		{"valid QP", mime + "Content-Transfer-Encoding: quoted-printable", "Line=20one=\r\nnext line=2E", "mime.transfer-encoding.1", "pass", "none"},
		{"RFC2369 comments", top + "\r\nList-Unsubscribe: (leave, please) <mailto:leave@example.test>, (web) <https://example.test/u_(list)>", "", "unsubscribe.list", "pass", "none"},
		{"unterminated URL comment", top + "\r\nList-Unsubscribe: <https://example.test/u> (missing", "", "unsubscribe.list", "fail", "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(test.header + "\r\n\r\n" + test.body)
			report, err := Analyze(context.Background(), Input{Raw: raw, StoredSize: int64(len(raw))})
			if err != nil {
				t.Fatal(err)
			}
			f := findingByID(report, test.id)
			if f.Outcome != test.outcome || f.Severity != test.severity {
				t.Errorf("%s = %s/%s: %s (want %s/%s)", test.id, f.Outcome, f.Severity, f.Detail, test.outcome, test.severity)
			}
		})
	}

	raw := []byte(top + "\r\nDKIM-Signature: broken\r\n" + dkim + "\r\n" + oneClick + "\r\n\r\n")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	f := findingByID(report, "unsubscribe.dkim-coverage")
	if f.Outcome != "observed" || len(f.Evidence) != 1 || f.Evidence[0].Occurrence == nil || *f.Evidence[0].Occurrence != 2 {
		t.Errorf("wrong DKIM source: %+v", f)
	}
}

func TestInspectionPlainTextURLs(t *testing.T) {
	raw := []byte("From: a@example.test\r\n\r\nSee (https://example.test/wiki/Article_(edition)). " +
		"Forward to http://[2001:db8::1]/inbox. Exact: <https://example.test/a.>.")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, r := range report.Resources {
		if r.Kind == "link" {
			urls = append(urls, r.URL)
		}
	}
	want := []string{"https://example.test/wiki/Article_(edition)", "http://[2001:db8::1]/inbox", "https://example.test/a."}
	if !slices.Equal(urls, want) {
		t.Errorf("got %q, want %q", urls, want)
	}
}

func TestInspectionARCStructure(t *testing.T) {
	aar := "ARC-Authentication-Results: (relay; original) i (instance) = 1; mx.example.test; none"
	ams := "ARC-Message-Signature: i=1; a=rsa-sha256; d=example.test; s=mail; h=From; bh=YWJj; b=YWJj"
	seal := "ARC-Seal: i (counter) = 01 (relay); a=rsa-sha256; d=example.test; s=mail; cv=none; b=YWJj"
	for _, tc := range []struct{ name, aar, ams, seal, want string }{
		{"first set", aar, ams, seal, "observed"},
		{"legacy received syntax", "ARC-Authentication-Results: mx.example.test i=1; spf=pass", ams, seal, "fail"},
		{"signed integer", strings.Replace(aar, "= 1", "= +1", 1), ams, seal, "fail"},
		{"wrong case", aar, strings.Replace(ams, "i=1", "I=1", 1), seal, "fail"},
		{"after other tags", aar, strings.Replace(ams, "i=1; a=rsa-sha256", "a=rsa-sha256; i=1", 1), seal, "fail"},
		{"prohibited header coverage", aar, strings.Replace(ams, "h=From", "h=From:ARC-Seal", 1), seal, "fail"},
		{"first seal claims pass", aar, ams, strings.Replace(seal, "cv=none", "cv=pass", 1), "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(strings.Join([]string{tc.seal, tc.ams, tc.aar, "From: sender@example.test", "", "Body"}, "\r\n"))
			report, err := Analyze(context.Background(), Input{Raw: raw})
			if err != nil {
				t.Fatal(err)
			}
			got := findingByID(report, "authentication.arc")
			if got.Outcome != tc.want {
				t.Errorf("got %+v, want %s", got, tc.want)
			}
		})
	}
}

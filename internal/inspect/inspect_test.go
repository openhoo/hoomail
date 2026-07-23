package inspect

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeEmailHTMLMatchesLegacyTransformations(t *testing.T) {
	input := `<html><head><base href="https://evil.test"><meta charset="utf-8"><script>alert(1)</script></head><body onload="steal()"><iframe src="bad">hidden</iframe><form action='javascript:send()'><input></form><a onclick=go() href=" javascript:alert(1)">bad</a><img onerror='oops()' src='data:text/html,pwn'><a href="https://safe.test">safe</a></body></html>`
	want := `<html><head></head><body><a href="#">bad</a><img src='#'><a href="https://safe.test">safe</a></body></html>`
	if got := SanitizeEmailHTML(input); got != want {
		t.Fatalf("sanitized HTML mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestRewriteCIDURLsMatchesNormalizedAndOriginalKeys(t *testing.T) {
	input := `<img src="cid:logo@example"><div style="background:url('cid:hero@example')"></div><img src='cid:missing@example'>`
	got := RewriteCIDURLs(input, map[string]int64{
		"logo@example": 12,
		"hero@example": 34,
	})
	want := `<img src="/api/attachments/12"><div style="background:url('/api/attachments/34')"></div><img src='cid:missing@example'>`
	if got != want {
		t.Fatalf("CID rewrite mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestBuildMIMETreeMatchesLegacyRawStructure(t *testing.T) {
	raw := strings.Join([]string{
		`Content-Type: multipart/mixed; boundary="outer"`,
		`Content-Transfer-Encoding: 7BIT`,
		``,
		`preamble`,
		`--outer`,
		`Content-Type: text/plain; charset="utf-8"`,
		``,
		`Hello`,
		`--outer`,
		`Content-Type: application/pdf; name="report.pdf"`,
		`Content-Disposition: attachment; filename="invoice.pdf"`,
		`Content-Transfer-Encoding: BASE64`,
		``,
		`YWJj`,
		`--outer--`,
		`epilogue`,
	}, "\r\n")

	got := BuildMIMETree(raw)
	if got.ContentType != "multipart/mixed" || got.Encoding == nil || *got.Encoding != "7bit" || len(got.Children) != 2 {
		t.Fatalf("unexpected root: %#v", got)
	}
	plain := got.Children[0]
	if plain.ContentType != "text/plain" || plain.Charset == nil || *plain.Charset != "utf-8" || plain.Size != len("Hello\r\n") {
		t.Fatalf("unexpected text node: %#v", plain)
	}
	attachment := got.Children[1]
	if attachment.ContentType != "application/pdf" || attachment.Disposition == nil || *attachment.Disposition != "attachment" || attachment.Filename == nil || *attachment.Filename != "invoice.pdf" || attachment.Encoding == nil || *attachment.Encoding != "base64" {
		t.Fatalf("unexpected attachment node: %#v", attachment)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"contentType"`, `"charset"`, `"encoding"`, `"disposition"`, `"filename"`, `"size"`, `"children"`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("JSON missing TypeScript field %s: %s", field, encoded)
		}
	}
}

func TestExtractLinksMatchesLegacyHTMLPrecedenceAndClassification(t *testing.T) {
	html := `<a href="https://example.test/path"><strong>Hello</strong> world</a><a href="https://example.test/path">duplicate</a><a href="#local">local</a><img src="https://img.test/hero.png" alt="Hero"><img src='https://track.test/p.gif' width='1'><img src="https://hidden.test/p.gif" style="visibility:hidden"><img src="cid:logo"><img src="data:image/png;base64,x">`
	text := `fallback https://text.test`
	got := ExtractLinks(&html, &text)
	want := []ExtractedLink{
		{Href: "https://example.test/path", Text: "Hello world", Kind: "link"},
		{Href: "https://img.test/hero.png", Text: "Hero", Kind: "image"},
		{Href: "https://track.test/p.gif", Text: "", Kind: "tracking-pixel"},
		{Href: "https://hidden.test/p.gif", Text: "", Kind: "tracking-pixel"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted links mismatch\nwant: %#v\n got: %#v", want, got)
	}

	emptyHTML := ""
	got = ExtractLinks(&emptyHTML, &text)
	want = []ExtractedLink{{Href: "https://text.test", Text: "", Kind: "link"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("text fallback mismatch: %#v", got)
	}
}

func TestRunHeaderChecksMatchesLegacyStatusesAndDetails(t *testing.T) {
	html := `<img src="https://img.test/a.png"><img src="https://track.test/p.gif" width="1" alt="pixel">`
	links := ExtractLinks(&html, nil)
	checks := RunHeaderChecks(HeaderCheckInput{
		Headers: map[string]string{
			"MESSAGE-ID":             "<id@example.test>",
			"Authentication-Results": "mx; spf=pass smtp.mailfrom=x; dkim=pass header.d=x",
			"DKIM-Signature":         "v=1",
			"List-Unsubscribe":       "<mailto:unsubscribe@example.test>",
		},
		HTML:  &html,
		Links: append(links, ExtractedLink{Href: "http://insecure.test", Kind: "link"}),
		Size:  103 * 1024,
	})
	want := []HeaderCheck{
		{ID: "message-id", Label: "Message-ID", Status: "pass", Detail: "Message-ID header present."},
		{ID: "auth-results", Label: "Authentication-Results", Status: "pass", Detail: "SPF: pass, DKIM: pass."},
		{ID: "dkim", Label: "DKIM-Signature", Status: "pass", Detail: "Message is DKIM-signed."},
		{ID: "unsubscribe", Label: "List-Unsubscribe", Status: "pass", Detail: "List-Unsubscribe header present (required for bulk mail by Gmail/Yahoo)."},
		{ID: "img-alt", Label: "Image alt text", Status: "warn", Detail: "1 image missing alt attributes (hurts accessibility and clipped rendering)."},
		{ID: "external-images", Label: "External images", Status: "info", Detail: "1 externally hosted image — blocked by default in many clients."},
		{ID: "tracking", Label: "Tracking pixels", Status: "warn", Detail: "1 likely tracking pixel detected."},
		{ID: "size", Label: "Message size", Status: "warn", Detail: "Message is 103 KB — Gmail clips messages over 102 KB."},
		{ID: "https", Label: "Link security", Status: "warn", Detail: "1 link uses plain HTTP instead of HTTPS."},
	}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("header checks mismatch\nwant: %#v\n got: %#v", want, checks)
	}
	encoded, err := json.Marshal(checks[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"id":"message-id","label":"Message-ID","status":"pass","detail":"Message-ID header present."}` {
		t.Fatalf("unexpected HeaderCheck JSON: %s", encoded)
	}
}

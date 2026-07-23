package inspect

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeEmailHTMLUsesParserAllowlist(t *testing.T) {
	input := `<html><head><base href="https://evil.test"><meta charset="utf-8"><link rel="stylesheet" href="https://evil.test/x.css"><style>@import url(https://evil.test/x.css)</style><script>alert(1)</script></head><body id="page" onload="steal()"><iframe src="bad"><b>hidden</b><iframe><form><input></form><svg><script>alert(2)</script><circle></circle></svg><math><mi>x</mi></math><table role="presentation" width="600" cellpadding="12" style="width: 600px; border-collapse: collapse; background-color: #ffffff; position: fixed; z-index: 9999; background-image: url(https://evil.test/bg.png)"><tr><td bgcolor="#fff" style="padding: 8px; color: rgb(1, 2, 3); behavior: url(x)">Hello &amp; welcome</td></tr></table><a name="legacy" href="https://safe.test/path?q=1" onclick="go()">safe</a><a href="mailto:user@example.test">mail</a><a href="#local">fragment</a><a href="/relative">relative</a><a href="javascript&#58;alert(1)">bad</a><img src="https://remote.test/pixel.png" onerror="oops()" alt="remote"><img src="cid:logo%40example.test" alt="logo"></body></html>`
	got := SanitizeEmailHTML(input, map[string]int64{"logo@example.test": 12})

	for _, retained := range []string{
		`<table role="presentation" width="600" cellpadding="12" style="width: 600px; border-collapse: collapse; background-color: #ffffff">`,
		`<td bgcolor="#fff" style="padding: 8px; color: rgb(1, 2, 3)">Hello &amp; welcome</td>`,
		`<a href="https://safe.test/path?q=1" target="_blank" rel="noopener noreferrer">safe</a>`,
		`<a href="mailto:user@example.test" target="_blank" rel="noopener noreferrer">mail</a>`,
		`<a>fragment</a>`, `<a>relative</a>`, `<a>bad</a>`,
		`<img alt="remote">`, `<img alt="logo" src="/api/attachments/12">`,
	} {
		if !strings.Contains(got, retained) {
			t.Errorf("sanitized HTML missing %q:\n%s", retained, got)
		}
	}
	for _, removed := range []string{"<base", "<meta", "<link", "<style", "<script", "<iframe", "<form", "<input", "<svg", "<math", "onload", "onclick", "onerror", " id=", " name=", "position", "z-index", "background-image", "behavior", "remote.test"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(removed)) {
			t.Errorf("sanitized HTML retained %q:\n%s", removed, got)
		}
	}
}

func TestSanitizeEmailHTMLRejectsRawAttachmentSourceButResolvesCID(t *testing.T) {
	input := `<img src="/api/attachments/12" alt="guessed"><img src="cid:logo@example.test" alt="logo">`
	got := SanitizeEmailHTML(input, map[string]int64{"logo@example.test": 12})
	want := `<img alt="guessed"><img alt="logo" src="/api/attachments/12">`
	if got != want {
		t.Fatalf("attachment source sanitization mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestSanitizeEmailHTMLRejectsMalformedActiveMarkupAndCSSFetches(t *testing.T) {
	input := `<p style="color: red; width: calc(100%); font-family: Arial, sans-serif; @import: x">before<iframe src=x><b>hidden</p><form><svg><math><p style="background-color: url(https://evil.test); color: expression(alert(1)); position: sticky; margin: 4px 8px">after</p>`
	got := SanitizeEmailHTML(input)
	lower := strings.ToLower(got)
	for _, removed := range []string{"<iframe", "<form", "<svg", "<math", "hidden", "url(", "expression", "sticky", "calc(", "evil.test"} {
		if strings.Contains(lower, removed) {
			t.Fatalf("active content %q survived: %s", removed, got)
		}
	}
	for _, retained := range []string{"before", "after", "color: red", "font-family: Arial, sans-serif", "margin: 4px 8px"} {
		if !strings.Contains(got, retained) {
			t.Fatalf("safe content %q was not retained: %s", retained, got)
		}
	}
}

func TestRewriteCIDURLsParsesQuotedAndUnquotedSources(t *testing.T) {
	input := `<img src=cid:logo%40example><img src='cid:%3Chero%40example%3E'><img src="cid:missing@example">`
	got := RewriteCIDURLs(input, map[string]int64{"logo@example": 12, "hero@example": 34})
	want := `<img src="/api/attachments/12"><img src="/api/attachments/34"><img src="cid:missing@example">`
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

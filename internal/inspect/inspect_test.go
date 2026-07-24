package inspect

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openhoo/hoomail/internal/mimeparse"
)

func TestSanitizeEmailHTMLUsesParserAllowlist(t *testing.T) {
	input := `<html><head><base href="https://evil.test"><meta charset="utf-8"><link rel="stylesheet" href="https://evil.test/x.css"><style>@import url(https://evil.test/x.css)</style><script>alert(1)</script></head><body id="page" onload="steal()"><iframe src="bad"><b>hidden</b><iframe><form><input></form><svg><script>alert(2)</script><circle></circle></svg><math><mi>x</mi></math><table role="presentation" width="600" cellpadding="12" style="width: 600px; border-collapse: collapse; background-color: #ffffff; position: fixed; z-index: 9999; background-image: url(https://evil.test/bg.png)"><tr><td bgcolor="#fff" style="padding: 8px; color: rgb(1, 2, 3); behavior: url(x)">Hello &amp; welcome</td></tr></table><a name="legacy" href="https://safe.test/path?q=1" onclick="bad()">safe</a><a href="mailto:user@example.test">mail</a><a href="#fragment">fragment</a><a href="relative">relative</a><a href="javascript:bad()">bad</a><img src="https://remote.test/a.png" alt="remote"><img src="cid:logo@example.test" alt="logo" onerror="bad()"></body></html>`
	got := SanitizeEmailHTML(input, map[string]int64{"logo@example.test": 12})
	for _, retained := range []string{`<table role="presentation" width="600" cellpadding="12" style="width: 600px; border-collapse: collapse; background-color: #ffffff">`, `<td bgcolor="#fff" style="padding: 8px; color: rgb(1, 2, 3)">Hello &amp; welcome</td>`, `<a href="https://safe.test/path?q=1" target="_blank" rel="noopener noreferrer">safe</a>`, `<img alt="logo" src="/api/attachments/12">`} {
		if !strings.Contains(got, retained) {
			t.Errorf("sanitized HTML missing %q:\n%s", retained, got)
		}
	}
	for _, removed := range []string{"<script", "<iframe", "<form", "<svg", "<math", "onload", "onclick", "position", "background-image", "remote.test"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(removed)) {
			t.Errorf("sanitized HTML retained %q:\n%s", removed, got)
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

func TestAnalyzeCompleteReportOrderingResourcesAndOfflineWording(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`Date: Fri, 24 Jul 2026 12:00:00 +0000`,
		`From: Alice <alice@example.test>`,
		`To: Bob <bob@example.test>`,
		`Message-ID: <clean@example.test>`,
		`MIME-Version: 1.0`,
		`Authentication-Results: mx.example.test; dkim=pass header.d=example.test`,
		`DKIM-Signature: v=1; a=rsa-sha256; b=YWJj; bh=YWJj; d=example.test; h=From:To:Subject:List-Unsubscribe:List-Unsubscribe-Post; s=mail`,
		`List-Unsubscribe: <mailto:leave@example.test>, <https://unroutable.invalid/u>`,
		`List-Unsubscribe-Post: List-Unsubscribe=One-Click`,
		`Content-Type: multipart/alternative; boundary="alt"`,
		``,
		`--alt`,
		`Content-Type: text/plain; charset=utf-8`,
		``,
		`Hello https://unroutable.invalid/plain`,
		`--alt`,
		`Content-Type: text/html; charset=utf-8`,
		``,
		`<html lang="en"><body><a href="http://unroutable.invalid/a"><img src="https://unroutable.invalid/p.gif" width="1" height="1" alt="Open"></a><img src="cid:logo@example.test"></body></html>`,
		`--alt--`,
		``,
	}, "\r\n"))
	report, err := Analyze(Input{Raw: raw, StoredSize: int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	if report.Analysis.Version != 1 || report.Analysis.State != "complete" || report.Analysis.Truncated || report.Analysis.ParsedThroughPath != nil {
		t.Fatalf("analysis=%#v", report.Analysis)
	}
	if report.MIMETree == nil || report.MIMETree.ContentType != "multipart/alternative" || len(report.MIMETree.Children) != 2 {
		t.Fatalf("mimeTree=%#v", report.MIMETree)
	}
	if len(report.Resources) < 3 {
		t.Fatalf("resources=%#v", report.Resources)
	}
	for index := 1; index < len(report.Findings); index++ {
		if findingLess(report.Findings[index], report.Findings[index-1]) {
			t.Fatalf("findings out of order at %d: %s before %s", index, report.Findings[index].ID, report.Findings[index-1].ID)
		}
	}
	for _, id := range []string{"analysis.parse", "message.date", "mime.alternative.1", "authentication.results.1", "authentication.dkim.1", "unsubscribe.one-click-pairing", "unsubscribe.dkim-coverage", "content.image-alt", "privacy.tracking-pixels", "compatibility.body-sizes"} {
		if !hasFinding(report, id) {
			t.Errorf("missing finding %s", id)
		}
	}
	for _, finding := range report.Findings {
		if strings.Contains(finding.ID, "authentication") || strings.Contains(finding.ID, "unsubscribe") {
			lower := strings.ToLower(finding.Detail)
			if strings.Contains(finding.ID, "dkim") && finding.Outcome != "fail" && !strings.Contains(lower, "not cryptographically verified") && !strings.Contains(lower, "not verified") {
				t.Errorf("unsafe DKIM wording: %#v", finding)
			}
		}
	}
	if got := summarize(report.Findings); !reflect.DeepEqual(got, report.Summary) {
		t.Fatalf("summary mismatch: %#v != %#v", report.Summary, got)
	}
}

func TestAnalyzeRawlessPartialUsesFallbackOnce(t *testing.T) {
	oldParse, oldHTML := parseMIME, analyzeHTMLPass
	t.Cleanup(func() { parseMIME, analyzeHTMLPass = oldParse, oldHTML })
	parseCalls, htmlCalls := 0, 0
	parseMIME = func(raw []byte, limits mimeparse.Limits) (mimeparse.Document, error) {
		parseCalls++
		return oldParse(raw, limits)
	}
	analyzeHTMLPass = func(raw []byte, path *string) (HTMLFacts, []string) { htmlCalls++; return oldHTML(raw, path) }
	htmlBody := `<html><body><a href="https://example.test">Example</a></body></html>`
	report, err := Analyze(Input{LegacyHTML: &htmlBody, StoredSize: int64(len(htmlBody))})
	if err != nil {
		t.Fatal(err)
	}
	if parseCalls != 1 || htmlCalls != 1 {
		t.Fatalf("parse=%d html=%d", parseCalls, htmlCalls)
	}
	if report.Analysis.State != "partial" || report.Analysis.Truncated || report.MIMETree != nil {
		t.Fatalf("analysis=%#v tree=%#v", report.Analysis, report.MIMETree)
	}
	wantUnavailable := []string{"message", "mime", "authentication", "unsubscribe"}
	if !reflect.DeepEqual(report.Analysis.UnavailableRuleFamilies, wantUnavailable) {
		t.Fatalf("unavailable=%#v", report.Analysis.UnavailableRuleFamilies)
	}
	if !hasFinding(report, "analysis.raw-unavailable") || findingByID(report, "analysis.parse").Outcome != "not-evaluated" || findingByID(report, "content.body").Outcome != "not-evaluated" {
		t.Fatalf("findings=%#v", report.Findings)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"parsedThroughPath":null`, `"mimeTree":null`, `"reference":null`, `"unavailableRuleFamilies":["message","mime","authentication","unsubscribe"]`} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("JSON missing %s: %s", expected, encoded)
		}
	}
}

func TestAnalyzePropagatesParserInvariantError(t *testing.T) {
	oldParse := parseMIME
	t.Cleanup(func() { parseMIME = oldParse })
	want := errors.New("correlation mismatch")
	parseMIME = func([]byte, mimeparse.Limits) (mimeparse.Document, error) { return mimeparse.Document{}, want }
	_, err := Analyze(Input{Raw: []byte("x")})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestAnalyzeHTMLPassZeroWithoutHTML(t *testing.T) {
	oldHTML := analyzeHTMLPass
	t.Cleanup(func() { analyzeHTMLPass = oldHTML })
	calls := 0
	analyzeHTMLPass = func(raw []byte, path *string) (HTMLFacts, []string) { calls++; return oldHTML(raw, path) }
	text := "plain"
	if _, err := Analyze(Input{LegacyText: &text}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("html calls=%d", calls)
	}
}

func TestHTMLFactsAggregatesOccurrencesAndCapsNodes(t *testing.T) {
	htmlBody := `<html lang="en"><body><a href="https://example.test"><img src="https://img.test/a.png" alt="Named"></a><a href="https://example.test"></a><img src="https://img.test/a.png" alt="Named"><img src="cid:logo"><img src="data:image/png;base64,AA=="></body></html>`
	facts, causes := analyzeHTML([]byte(htmlBody), new("1.2"))
	if len(causes) != 0 || facts.anchorCount != 2 || facts.unnamedLinks != 1 || facts.imageCount != 4 || facts.externalImages != 2 {
		t.Fatalf("facts=%#v causes=%#v", facts, causes)
	}
	a := analyzer{resourceMap: map[string]int{}, causes: map[string]struct{}{}}
	for _, item := range facts.resources {
		a.addResource(item.kind, item.path, item.url, item.text)
	}
	found := false
	for _, item := range a.resources {
		if item.Kind == "image" && item.URL == "https://img.test/a.png" {
			found = item.OccurrenceCount == 2
		}
	}
	if !found {
		t.Fatalf("resources=%#v", a.resources)
	}

	var huge strings.Builder
	huge.WriteString("<div>")
	for range MaxHTMLNodes {
		huge.WriteString("<span></span>")
	}
	huge.WriteString("<span></span></div>")
	limited, limitedCauses := analyzeHTML([]byte(huge.String()), nil)
	if !limited.truncated || !containsString(limitedCauses, "HTML nodes") {
		t.Fatalf("limited=%#v causes=%#v", limited, limitedCauses)
	}
}

func TestAnalyzeHTMLResolvesNestedLabelAndDetectsStyledHiddenTrackers(t *testing.T) {
	htmlBody := `<html lang="en"><body><span id="label"><strong>Open account</strong></span><a href="https://example.test" aria-labelledby="label"></a><div style="display:none"><img src="https://tracker.test/parent.gif" alt=""></div><img src="https://tracker.test/style.gif" style="width: 1px; height:1px" alt=""></body></html>`
	facts, causes := analyzeHTML([]byte(htmlBody), new("1"))
	if len(causes) != 0 {
		t.Fatalf("causes=%#v", causes)
	}
	if facts.unnamedLinks != 0 {
		t.Fatalf("nested aria-labelledby was not resolved: %#v", facts)
	}
	if facts.trackingPixels != 2 {
		t.Fatalf("trackingPixels=%d want 2", facts.trackingPixels)
	}
	if len(facts.resources) != 3 || facts.resources[0].kind != "tracking-pixel" || facts.resources[1].kind != "tracking-pixel" || facts.resources[2].text != "Open account" {
		t.Fatalf("resources=%#v", facts.resources)
	}
}

func TestAnalyzeTruncatedRawIndexDoesNotEmitPerPartSemanticFindings(t *testing.T) {
	oldParse := parseMIME
	t.Cleanup(func() { parseMIME = oldParse })
	path := "1"
	parseMIME = func(raw []byte, _ mimeparse.Limits) (mimeparse.Document, error) {
		return mimeparse.Document{
			Raw: raw,
			Root: &mimeparse.Node{
				Path:             path,
				MediaType:        "text/plain",
				TransferEncoding: "base64",
				RawHeader:        mimeparse.ByteRange{Start: 0, End: 16},
				RawBody:          mimeparse.ByteRange{Start: 18, End: len(raw)},
			},
			ParsedThroughPath: &path,
			Truncated:         true,
			TruncationCauses:  []string{"physical lines"},
		}, nil
	}
	report, err := Analyze(Input{Raw: []byte("Subject: capped\r\n\r\nnot base64")})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(report.Analysis.UnavailableRuleFamilies, "mime") || !hasFinding(report, "mime.structure") {
		t.Fatalf("analysis=%#v findings=%#v", report.Analysis, report.Findings)
	}
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.ID, "mime.content-type.") || strings.HasPrefix(finding.ID, "mime.transfer-encoding.") || strings.HasPrefix(finding.ID, "mime.charset.") {
			t.Fatalf("truncated raw-only node produced semantic finding: %#v", finding)
		}
	}
}

func TestAnalyzeStandardsEdgeCases(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`Date: Fri, 24 Jul 2026 12:00:00 +0000`,
		`From: Automated System:;`,
		`Authentication-Results: mx.example; dkim=pass reason="good signature" header.d=example.test`,
		`List-Unsubscribe: <https://example.test/u?ids=a,b>`,
		`Content-Type: text/plain`,
		`Content-Transfer-Encoding: base64`,
		``,
		`Y2Fmw6k=`,
	}, "\r\n"))
	report, err := Analyze(Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if findingByID(report, "message.from").Outcome != "pass" || findingByID(report, "message.message-id").Severity != "warning" {
		t.Fatalf("message findings=%#v", report.Findings)
	}
	if findingByID(report, "authentication.results.1").Outcome != "observed" || findingByID(report, "unsubscribe.list").Outcome != "pass" {
		t.Fatalf("auth/unsubscribe findings=%#v", report.Findings)
	}
	if findingByID(report, "mime.charset.1").Outcome != "fail" {
		t.Fatalf("charset=%#v", findingByID(report, "mime.charset.1"))
	}
}

func TestAnalyzeRejectsMalformedOneClickHTTPSURL(t *testing.T) {
	fields := []headerOccurrence{{value: `<https:/unsubscribe>`}}
	if _, ok := parseListUnsubscribe(fields); ok {
		t.Fatal("malformed HTTPS URL accepted")
	}
}

func TestAnalyzeAttachmentOnlyRootIsResourceAndEmptyBodyHasZeroSize(t *testing.T) {
	raw := []byte("Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=x.pdf\r\n\r\n")
	report, err := Analyze(Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Resources) != 1 || report.Resources[0].Kind != "attachment" || report.Resources[0].Text != "x.pdf" {
		t.Fatalf("resources=%#v", report.Resources)
	}
	if report.MIMETree == nil || report.MIMETree.RawSize == nil || *report.MIMETree.RawSize != 0 {
		t.Fatalf("mimeTree=%#v", report.MIMETree)
	}
}

func TestAnalyzeHiddenTextDoesNotNameLink(t *testing.T) {
	facts, _ := analyzeHTML([]byte(`<a href="https://example.test"><span hidden>secret</span></a>`), nil)
	if facts.unnamedLinks != 1 {
		t.Fatalf("facts=%#v", facts)
	}
}

func TestFindingOrderingAndSummaryBuckets(t *testing.T) {
	items := []Finding{
		finding("content.pass", "content", "pass", "none", "standard", "all", "", "", nil, nil),
		finding("message.observe", "message", "observed", "none", "evidence", "all", "", "", nil, nil),
		finding("message.warn", "message", "pass", "warning", "recommendation", "all", "", "", nil, nil),
		finding("message.fail", "message", "fail", "error", "standard", "all", "", "", nil, nil),
		finding("message.advisory", "message", "pass", "advisory", "heuristic", "all", "", "", nil, nil),
		finding("message.unknown", "message", "not-evaluated", "none", "evidence", "unknown", "", "", nil, nil),
	}
	sortFindings(items)
	want := []string{"message.fail", "message.warn", "message.advisory", "message.unknown", "message.observe", "content.pass"}
	for index, id := range want {
		if items[index].ID != id {
			t.Fatalf("order[%d]=%s want %s", index, items[index].ID, id)
		}
	}
	summary := summarize(items)
	if summary.Fail != 1 || summary.Warning != 1 || summary.Advisory != 1 || summary.NotEvaluated != 1 || summary.Observed != 1 || summary.Pass != 1 {
		t.Fatalf("summary=%#v", summary)
	}
}

func hasFinding(report Report, id string) bool { return findingByID(report, id).ID != "" }
func findingByID(report Report, id string) Finding {
	for _, finding := range report.Findings {
		if finding.ID == id {
			return finding
		}
	}
	return Finding{}
}
func sortFindings(items []Finding) {
	for index := 1; index < len(items); index++ {
		for current := index; current > 0 && findingLess(items[current], items[current-1]); current-- {
			items[current], items[current-1] = items[current-1], items[current]
		}
	}
}

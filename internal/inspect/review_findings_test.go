package inspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openhoo/hoomail/internal/mimeparse"
)

func TestAnalyzeHTMLIndexedStackMatchingGolden(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<div id="label">Open <span>account</span></div>`)
	body.WriteString(`<a href="https://example.test/one" aria-labelledby="label"></a>`)
	body.WriteString(`<a href="https://example.test/two"></a></ghost></div>`)
	facts, causes, err := analyzeHTML(context.Background(), []byte(body.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(causes) != 0 {
		t.Fatalf("causes=%v", causes)
	}
	if facts.anchorCount != 2 || facts.unnamedLinks != 1 {
		t.Fatalf("anchors=%d unnamed=%d", facts.anchorCount, facts.unnamedLinks)
	}

	const depth = 30000
	var deep strings.Builder
	deep.Grow(depth * 12)
	for range depth {
		deep.WriteString(`<x id="a">`)
	}
	for range 20000 {
		deep.WriteString("x")
	}
	for range depth {
		deep.WriteString(`</x>`)
	}
	deep.WriteString(strings.Repeat(`</unmatched>`, 2000))
	deepFacts, deepCauses, err := analyzeHTML(context.Background(), []byte(deep.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if deepFacts.truncated || len(deepCauses) != 0 {
		t.Fatalf("deep facts truncated=%v causes=%v", deepFacts.truncated, deepCauses)
	}
}

func TestAnalyzeHTMLBoundsEvidenceAndResourcesDuringCollection(t *testing.T) {
	const repeated = MaxEvidencePerFinding + 100
	var repeatedHTML strings.Builder
	for range repeated {
		repeatedHTML.WriteString(`<img src="https://img.example.test/repeated.png">`)
	}
	facts, causes, err := analyzeHTML(context.Background(), []byte(repeatedHTML.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(causes) != 0 {
		t.Fatalf("causes=%v", causes)
	}
	if facts.missingAlt != repeated || len(facts.missingAltEvidence) != MaxEvidencePerFinding {
		t.Fatalf("missing alt count=%d evidence=%d", facts.missingAlt, len(facts.missingAltEvidence))
	}
	if len(facts.resources) != 1 || facts.resources[0].count != repeated {
		t.Fatalf("resources=%#v", facts.resources)
	}

	var distinctHTML strings.Builder
	for i := 0; i < MaxResources+25; i++ {
		fmt.Fprintf(&distinctHTML, `<img src="https://img.example.test/%d.png" alt="">`, i)
	}
	distinct, _, err := analyzeHTML(context.Background(), []byte(distinctHTML.String()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(distinct.resources) != MaxResources || !distinct.resourcesCapped {
		t.Fatalf("resource cap len=%d capped=%v", len(distinct.resources), distinct.resourcesCapped)
	}
}

func TestBuildMIMETreeOmitsChecksumsForRawOnlyTruncation(t *testing.T) {
	oldParse := parseMIME
	t.Cleanup(func() { parseMIME = oldParse })

	run := func(document mimeparse.Document) *MimeNode {
		parseMIME = func(raw []byte, _ mimeparse.Limits) (mimeparse.Document, error) {
			document.Raw = raw
			return document, nil
		}
		report, err := Analyze(context.Background(), Input{Raw: []byte("body")})
		if err != nil {
			t.Fatal(err)
		}
		return report.MIMETree
	}

	truncatedTree := run(mimeparse.Document{
		Root:      &mimeparse.Node{Path: "1", MediaType: "text/plain", RawBody: mimeparse.ByteRange{Start: 0, End: 4}},
		Truncated: true, TruncationCauses: []string{"physical lines"},
	})
	if truncatedTree == nil || truncatedTree.RawSize == nil || truncatedTree.DecodedSize != nil || truncatedTree.Checksums != nil {
		t.Fatalf("truncated tree=%#v", truncatedTree)
	}

	complete := run(mimeparse.Document{
		Root: &mimeparse.Node{Path: "1", MediaType: "text/plain", RawBody: mimeparse.ByteRange{Start: 0, End: 4}, DecodedBody: []byte{}},
	})
	if complete == nil || complete.DecodedSize == nil || *complete.DecodedSize != 0 || complete.Checksums == nil {
		t.Fatalf("complete tree=%#v", complete)
	}
}

func TestTrimReportBoundsEscapedSearchableHeaders(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("From: sender@example.test\r\nTo: recipient@example.test\r\n")
	for i := range 120 {
		fmt.Fprintf(&raw, "X-Bulk-%03d: %s\r\n", i, strings.Repeat("<", 8000))
	}
	raw.WriteString("\r\nbody")

	report, err := Analyze(context.Background(), Input{Raw: []byte(raw.String())})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxReportBytes {
		t.Fatalf("encoded report=%d exceeds cap %d", len(encoded), MaxReportBytes)
	}
	if !report.Analysis.Truncated || len(report.Headers) >= 120 {
		t.Fatalf("analysis=%#v headers=%d", report.Analysis, len(report.Headers))
	}
	truncated := findingByID(report, "analysis.truncated")
	if truncated.ID == "" || !strings.Contains(truncated.Detail, "report bytes") {
		t.Fatalf("truncation finding=%#v", truncated)
	}
}

func TestAnalyzePropagatesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Analyze(ctx, Input{Raw: []byte("Subject: cancelled\r\n\r\nbody")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestAnalyzeReportsSkippedSemanticParseWhenBounded(t *testing.T) {
	orig := mimeparse.InspectionLimits
	mimeparse.InspectionLimits.MaxPhysicalLines = 8
	defer func() { mimeparse.InspectionLimits = orig }()

	raw := []byte("From: a@example.test\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n" +
		"\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\none\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\ntwo\r\n" +
		"--b--\r\n")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	finding := findingByID(report, "analysis.parse")
	if finding.ID == "" || finding.Outcome != "not-evaluated" {
		t.Fatalf("analysis.parse = %+v, want not-evaluated", finding)
	}
}
func TestAnalyzeRetainsTruncationFindingWhenBounded(t *testing.T) {
	orig := mimeparse.InspectionLimits
	mimeparse.InspectionLimits.MaxPhysicalLines = 8
	defer func() { mimeparse.InspectionLimits = orig }()

	raw := []byte("From: a@example.test\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n" +
		"\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\none\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\ntwo\r\n" +
		"--b--\r\n")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Analysis.Truncated {
		t.Fatalf("analysis=%#v, want truncated", report.Analysis)
	}
	truncated := findingByID(report, "analysis.truncated")
	if truncated.ID == "" {
		t.Fatalf("missing analysis.truncated finding in %+v", report.Findings)
	}
	if truncated.Outcome != "observed" {
		t.Fatalf("analysis.truncated outcome=%q, want observed", truncated.Outcome)
	}
	if !strings.Contains(truncated.Detail, "physical lines") {
		t.Fatalf("analysis.truncated detail=%q, want physical lines cause", truncated.Detail)
	}
}

package inspect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeReviewEncodedMessageDoesNotInventRawRanges(t *testing.T) {
	inner := "Content-Type: multipart/mixed; boundary=i\r\n\r\n--i\r\nContent-Type: text/plain\r\n\r\nbody\r\n--i--\r\n"
	raw := []byte("MIME-Version: 1.0\r\nContent-Type: message/rfc822\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(inner)))
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if findingByID(report, "mime.structure").Outcome == "fail" {
		t.Fatalf("decoded valid multipart reported a boundary defect: %+v", findingByID(report, "mime.structure"))
	}
	if report.MIMETree.Children[0].RawSize != nil {
		t.Fatalf("decoded-only child invented a raw byte range: %+v", report.MIMETree.Children[0])
	}
}

func TestSanitizeSVGReviewRejectsEscapedExternalPaint(t *testing.T) {
	raw := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect fill="u\72l(https://evil.example/image.svg#paint)" width="10" height="10"/></svg>`)
	clean, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(clean), "evil.example") {
		t.Fatalf("escaped external CSS URL survived static SVG sanitizer: %s", clean)
	}
}

func TestAnalyzeReviewHeaderLoneCRIsProhibitedControl(t *testing.T) {
	raw := []byte("X-Header: safe\rX-Fake: hidden\r\n\r\nbody")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingByID(report, "message.header-syntax"); got.Outcome != "fail" {
		t.Fatalf("lone CR header control escaped syntax inspection: %+v", got)
	}
}

func TestAnalyzeReviewNestedHeaderControlUsesOriginalSourceLine(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\nX-Header: safe\rX-Fake: hidden\r\n\r\nbody\r\n--b--\r\n")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	got := findingByID(report, "mime.header-syntax.1.1")
	if got.Outcome != "fail" || len(got.Evidence) != 1 || got.Evidence[0].Line == nil || *got.Evidence[0].Line != 5 {
		t.Fatalf("nested header control reported wrong original-source line: %+v", got)
	}
}

func TestAnalyzeReviewIncompletePartIsSenderDefect(t *testing.T) {
	report, err := Analyze(context.Background(), Input{Raw: []byte("Content-TYpe: multipArt/0;BoundArY=b\n\n--b\n")})
	if err != nil {
		t.Fatalf("incomplete sender MIME returned an internal analyzer error: %v", err)
	}
	if report.Analysis.State != "partial" || findingByID(report, "analysis.parse").Outcome != "fail" {
		t.Fatalf("incomplete MIME report state=%s parse=%+v", report.Analysis.State, findingByID(report, "analysis.parse"))
	}
}

func TestAnalyzeReviewBoundsEscapedMIMETreeMetadata(t *testing.T) {
	raw := []byte("Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"" + strings.Repeat("<", 800000) + "\"\r\n\r\nattachment")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatalf("bounded sender MIME metadata returned internal error: %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > MaxReportBytes || !report.Analysis.Truncated {
		t.Fatalf("report error=%v bytes=%d truncated=%v", err, len(encoded), report.Analysis.Truncated)
	}
	if !strings.Contains(findingByID(report, "analysis.truncated").Detail, "report bytes") {
		t.Fatalf("missing report-byte truncation evidence: %+v", report.Analysis)
	}
}

func TestAnalyzeReviewRootEncapsulatedAttachmentResourcePreservesMessage(t *testing.T) {
	raw := []byte("Content-Type: message/rfc822\r\nContent-Disposition: attachment; filename=attached.eml\r\n\r\nSubject: inner\r\n\r\nbody")
	report, err := Analyze(context.Background(), Input{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Resources) != 1 || report.Resources[0].Kind != "attachment" || report.Resources[0].Text != "attached.eml" || pointerValue(report.Resources[0].Path) != "1" {
		t.Fatalf("root message attachment resource lost wrapper identity: %+v", report.Resources)
	}
}

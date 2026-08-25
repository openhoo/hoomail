package inspect

import (
	"context"
	"strings"
	"testing"
)

func TestParseARCInstanceFromAARAcceptsRFC8617Form(t *testing.T) {
	value := "i=1; mx.google.com;\r\n\tspf=pass smtp.mailfrom=example.test\r\n\tdkim=pass header.d=example.test"
	instance, ok := parseARCInstanceFromAAR(value)
	if !ok || instance != 1 {
		t.Fatalf("instance=%d ok=%v, want instance 1 accepted", instance, ok)
	}
	authserv, valid := parseAuthenticationResults("mx.google.com;\r\n\tspf=pass smtp.mailfrom=example.test\r\n\tdkim=pass header.d=example.test")
	if !valid || authserv != "mx.google.com" {
		t.Fatalf("authserv=%q valid=%v, want mx.google.com accepted", authserv, valid)
	}
}

func TestParseARCInstanceFromAARRetainsLegacyAndRejectsMalformed(t *testing.T) {
	if instance, ok := parseARCInstanceFromAAR("mx.example.test i=2; spf=pass"); !ok || instance != 2 {
		t.Fatalf("legacy form rejected: instance=%d ok=%v", instance, ok)
	}
	for _, malformed := range []string{
		"i=x; mx.example.test; spf=pass",
		"i=1",
		"i=1;",
		"mx.example.test; spf=pass",
		"i=1; mx.example.test; noequals",
		"i=0; mx.example.test; spf=pass",
		"i=51; mx.example.test; spf=pass",
	} {
		if _, ok := parseARCInstanceFromAAR(malformed); ok {
			t.Fatalf("malformed AAR accepted: %q", malformed)
		}
	}
}

func TestAnalyzeAcceptsRFC8617AARInstanceTag(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"Date: Fri, 24 Jul 2026 12:00:00 +0000",
		"From: Alice <alice@example.test>",
		"To: Bob <bob@example.test>",
		"Message-ID: <arc@example.test>",
		"ARC-Authentication-Results: i=1; mx.example.test; spf=pass smtp.mailfrom=example.test",
		"",
		"Hello",
		"",
	}, "\r\n"))
	report, err := Analyze(context.Background(), Input{Raw: raw, StoredSize: int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	arc := findingByID(report, "authentication.arc")
	if arc.ID == "" || strings.Contains(arc.Detail, "malformed syntax") {
		t.Fatalf("RFC 8617 AAR rejected as malformed: %#v", arc)
	}
	if !strings.Contains(arc.Detail, "gap or an incomplete set") {
		t.Fatalf("unexpected ARC detail: %#v", arc)
	}
}

func TestAnalyzeHTMLCompatibilityMarksStyleClippingTruncated(t *testing.T) {
	clean := analyzeHTMLCompatibility([]byte(`<style>a{color:red}</style><p>Hi</p>`))
	if clean == nil || clean.Truncated {
		t.Fatalf("clean compatibility marked truncated: %#v", clean)
	}
	first := "<style>" + strings.Repeat("a", MaxHTMLTokenBytes/2) + "</style>"
	second := "<style>" + strings.Repeat("b", MaxHTMLTokenBytes/2+16) + "</style>"
	clipped := analyzeHTMLCompatibility([]byte(first + second))
	if clipped == nil || !clipped.Truncated {
		t.Fatalf("clipped style block not marked truncated: %#v", clipped)
	}
}

func TestAnalyzeNodeCapMarksCompatibilityUnavailable(t *testing.T) {
	var body strings.Builder
	body.WriteString("<html><body><div>")
	for range MaxHTMLNodes {
		body.WriteString("<span></span>")
	}
	body.WriteString("<span></span></div></body></html>")
	raw := []byte(strings.Join([]string{
		"Date: Fri, 24 Jul 2026 12:00:00 +0000",
		"From: Alice <alice@example.test>",
		"To: Bob <bob@example.test>",
		"Message-ID: <nodes@example.test>",
		"Content-Type: text/html; charset=utf-8",
		"",
		body.String(),
		"",
	}, "\r\n"))
	report, err := Analyze(context.Background(), Input{Raw: raw, StoredSize: int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Analysis.Truncated {
		t.Fatalf("analysis not truncated: %#v", report.Analysis)
	}
	found := false
	for _, family := range report.Analysis.UnavailableRuleFamilies {
		if family == "compatibility" {
			found = true
		}
	}
	if !found {
		t.Fatalf("compatibility family missing: %#v", report.Analysis.UnavailableRuleFamilies)
	}
	truncatedFinding := findingByID(report, "analysis.truncated")
	if truncatedFinding.ID == "" || !strings.Contains(truncatedFinding.Detail, "HTML nodes") {
		t.Fatalf("truncation finding=%#v", truncatedFinding)
	}
	if report.HTMLCompatibility == nil || !report.HTMLCompatibility.Truncated {
		t.Fatalf("compatibility=%#v", report.HTMLCompatibility)
	}
}

func TestInternationalizedHeadersEvidenceCountsOccurrencesPerField(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"Date: Fri, 24 Jul 2026 12:00:00 +0000",
		"From: Alice <alice@example.test>",
		"To: Bob <bob@example.test>",
		"Message-ID: <i18n@example.test>",
		"Subject: première annonce",
		"Subject: deuxième annonce",
		"",
		"Hello",
		"",
	}, "\r\n"))
	report, err := Analyze(context.Background(), Input{Raw: raw, StoredSize: int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	i18n := findingByID(report, "message.internationalized-headers")
	if i18n.ID == "" || len(i18n.Evidence) != 2 {
		t.Fatalf("finding=%#v evidence=%d", i18n, len(i18n.Evidence))
	}
	for index, want := range []int{1, 2} {
		ev := i18n.Evidence[index]
		if ev.Field == nil || *ev.Field != "Subject" || ev.Occurrence == nil || *ev.Occurrence != want {
			t.Fatalf("evidence[%d]=%#v, want Subject occurrence %d", index, ev, want)
		}
	}
}

func TestLineEndingsRuleFlagsBareLFOnly(t *testing.T) {
	base := []string{
		"Date: Fri, 24 Jul 2026 12:00:00 +0000",
		"From: Alice <alice@example.test>",
		"To: Bob <bob@example.test>",
		"Message-ID: <lineendings@example.test>",
	}

	crlf := []byte(strings.Join(append(append([]string{}, base...), "", "Hello", ""), "\r\n"))
	report, err := Analyze(context.Background(), Input{Raw: crlf, StoredSize: int64(len(crlf))})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingByID(report, "message.line-endings"); got.Outcome != "pass" {
		t.Fatalf("CRLF-only message line-endings outcome=%q detail=%q, want pass", got.Outcome, got.Detail)
	}

	bareLF := []byte(strings.Join(append(append([]string{}, base...), "", "Hello", ""), "\n"))
	report, err = Analyze(context.Background(), Input{Raw: bareLF, StoredSize: int64(len(bareLF))})
	if err != nil {
		t.Fatal(err)
	}
	got := findingByID(report, "message.line-endings")
	if got.Outcome != "fail" || !strings.Contains(got.Detail, "Bare LF") {
		t.Fatalf("bare-LF message line-endings outcome=%q detail=%q, want fail mentioning bare LF", got.Outcome, got.Detail)
	}

	loneCR := []byte("Date: Fri, 24 Jul 2026 12:00:00 +0000\rFrom: Alice <alice@example.test>\r\nTo: Bob <bob@example.test>\r\n\r\nbody with lone\rcarriage return\r\n")
	if _, err = Analyze(context.Background(), Input{Raw: loneCR, StoredSize: int64(len(loneCR))}); err != nil {
		t.Fatalf("lone-CR content must not hard-fail analysis: %v", err)
	}
}

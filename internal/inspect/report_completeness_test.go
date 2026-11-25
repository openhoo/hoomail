package inspect

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openhoo/hoomail/internal/mimeparse"
)

func TestClippedFallbackReportsOnlyWhatWasInspected(t *testing.T) {
	start := `<a href="https://example.test" aria-labelledby="label"></a>`
	html := start + `<span id="label">News</span><img src="https://example.test/image">`
	old := mimeparse.InspectionLimits
	mimeparse.InspectionLimits.MaxRawBytes = len(start)
	t.Cleanup(func() { mimeparse.InspectionLimits = old })
	report, err := Analyze(context.Background(), Input{LegacyHTML: &html})
	if err != nil {
		t.Fatal(err)
	}
	if report.Analysis.State != "partial" || report.HTMLCompatibility == nil || !report.HTMLCompatibility.Truncated {
		t.Fatalf("incomplete markup: %+v", report.Analysis)
	}
	for _, id := range []string{"content.link-name", "privacy.external-images", "privacy.tracking-pixels", "privacy.insecure-links"} {
		if hasFinding(report, id) {
			t.Errorf("unprovable finding %s in a clipped prefix", id)
		}
	}
	if !strings.Contains(findingByID(report, "compatibility.body-sizes").Detail, fmt.Sprintf("stored fallback HTML bytes: %d", len(html))) {
		t.Fatal("body size must count the full stored representation")
	}
}

func TestIncompleteHeaderBlockDoesNotClaimChecksRan(t *testing.T) {
	report, err := Analyze(context.Background(), Input{Raw: []byte("From: sender@example.test")})
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"message", "mime", "authentication", "unsubscribe"} {
		if !containsString(report.Analysis.UnavailableRuleFamilies, family) {
			t.Errorf("unavailable family missing: %s", family)
		}
	}
	if hasFinding(report, "message.date") || report.MIMETree != nil {
		t.Fatal("absence of an unindexed field is not evidence")
	}
}

func TestMultipartNeverClaimsDecodedLeafChecksum(t *testing.T) {
	report, err := Analyze(context.Background(), Input{Raw: []byte("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x--\r\n")})
	if err != nil {
		t.Fatal(err)
	}
	if findingByID(report, "mime.structure").Outcome != "fail" {
		t.Error("multipart with no body parts cannot pass structural validation")
	}
	root := report.MIMETree
	if root == nil || root.DecodedSize != nil || root.Checksums != nil {
		t.Fatalf("empty multipart is not a decoded leaf: %+v", root)
	}
}

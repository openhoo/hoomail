package inspect

import (
	"strconv"
	"strings"
	"testing"
)

var (
	benchmarkStringSink string
	benchmarkReportSink Report
)

func BenchmarkSanitizeEmailHTML(b *testing.B) {
	fixtures := []struct {
		name string
		html string
	}{
		{name: "Newsletter64Sections", html: benchmarkNewsletterHTML(64)},
		{name: "Newsletter256Sections", html: benchmarkNewsletterHTML(256)},
	}

	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.html)))
			for b.Loop() {
				benchmarkStringSink = SanitizeEmailHTML(fixture.html)
			}
		})
	}
}

func BenchmarkRewriteCIDURLs(b *testing.B) {
	fixtures := []struct {
		name   string
		html   string
		cidMap map[string]int64
	}{
		benchmarkCIDFixture("InlineImages128", 128),
		benchmarkCIDFixture("InlineImages512", 512),
	}

	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.html)))
			for b.Loop() {
				benchmarkStringSink = RewriteCIDURLs(fixture.html, fixture.cidMap)
			}
		})
	}
}

func BenchmarkAnalyze(b *testing.B) {
	fixtures := []struct {
		name string
		raw  []byte
	}{
		{name: "Mixed32Attachments", raw: []byte(benchmarkMIMEMessage(32, false))},
		{name: "NestedMixed64Attachments", raw: []byte(benchmarkMIMEMessage(64, true))},
		{name: "Campaign512Links", raw: []byte(benchmarkAnalysisMessage(benchmarkCampaignHTML(512)))},
		{name: "CapSaturatingResources", raw: []byte(benchmarkAnalysisMessage(benchmarkCampaignHTML(MaxResources + 256)))},
	}
	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.raw)))
			for b.Loop() {
				report, err := Analyze(Input{Raw: fixture.raw, StoredSize: int64(len(fixture.raw))})
				if err != nil {
					b.Fatal(err)
				}
				benchmarkReportSink = report
			}
		})
	}
}

func benchmarkAnalysisMessage(htmlBody string) string {
	return strings.Join([]string{
		"Date: Fri, 24 Jul 2026 12:00:00 +0000",
		"From: Sender <sender@example.test>",
		"To: Recipient <recipient@example.test>",
		"Message-ID: <benchmark@example.test>",
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=utf-8",
		"",
		htmlBody,
		"",
	}, "\r\n")
}

func benchmarkNewsletterHTML(sectionCount int) string {
	var html strings.Builder
	html.Grow(sectionCount * 640)
	html.WriteString(`<!doctype html><html><head><meta charset="utf-8"><base href="https://evil.example"><script>window.tracker=true</script></head><body onload="track()"><table role="presentation">`)
	for index := range sectionCount {
		number := strconv.Itoa(index)
		html.WriteString(`<tr onclick="openSection()"><td><h2>Monthly update `)
		html.WriteString(number)
		html.WriteString(`</h2><p>Product news, customer stories, release notes, and practical guidance for teams evaluating this month's changes.</p><a href="https://example.test/articles/`)
		html.WriteString(number)
		html.WriteString(`" onmouseover='trackHover()'>Read the complete article</a><img src="https://cdn.example.test/images/`)
		html.WriteString(number)
		html.WriteString(`.jpg" onerror=replaceImage() alt="Article illustration"><form action="javascript:steal()"><input name="email"></form><iframe src="https://tracker.example.test/frame/`)
		html.WriteString(number)
		html.WriteString(`">tracking fallback</iframe></td></tr>`)
	}
	html.WriteString(`</table></body></html>`)
	return html.String()
}

func benchmarkCIDFixture(name string, imageCount int) struct {
	name   string
	html   string
	cidMap map[string]int64
} {
	cidMap := make(map[string]int64, imageCount)
	var html strings.Builder
	html.Grow(imageCount * 180)
	html.WriteString(`<html><body><table role="presentation">`)
	for index := range imageCount {
		number := strconv.Itoa(index)
		cid := "asset-" + number + "@mail.example"
		cidMap[cid] = int64(10000 + index)
		html.WriteString(`<tr><td><img src="cid:`)
		html.WriteString(cid)
		html.WriteString(`" alt="Product image `)
		html.WriteString(number)
		html.WriteString(`"></td><td style="background-image:url('cid:`)
		html.WriteString(cid)
		html.WriteString(`')">Offer details and call to action</td></tr>`)
	}
	html.WriteString(`<tr><td><img src="cid:unresolved@mail.example" alt="Remote fallback"></td></tr></table></body></html>`)
	return struct {
		name   string
		html   string
		cidMap map[string]int64
	}{name: name, html: html.String(), cidMap: cidMap}
}

func benchmarkMIMEMessage(attachmentCount int, nested bool) string {
	var raw strings.Builder
	raw.Grow(attachmentCount * 1800)
	raw.WriteString("Content-Type: multipart/mixed; boundary=\"outer-boundary\"\r\nContent-Transfer-Encoding: 7bit\r\n\r\n")
	if nested {
		raw.WriteString("--outer-boundary\r\nContent-Type: multipart/alternative; boundary=\"alternative-boundary\"\r\n\r\n")
		raw.WriteString("--alternative-boundary\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		raw.WriteString(strings.Repeat("This is the plain text campaign summary with product updates.\r\n", 64))
		raw.WriteString("--alternative-boundary\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<html><body>")
		raw.WriteString(strings.Repeat("<p>Detailed campaign content and customer guidance.</p>", 64))
		raw.WriteString("</body></html>\r\n--alternative-boundary--\r\n")
	} else {
		raw.WriteString("--outer-boundary\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<html><body>")
		raw.WriteString(strings.Repeat("<p>Detailed campaign content and customer guidance.</p>", 128))
		raw.WriteString("</body></html>\r\n")
	}
	payload := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=\r\n", 32)
	for index := range attachmentCount {
		number := strconv.Itoa(index)
		raw.WriteString("--outer-boundary\r\nContent-Type: application/pdf; name=\"report-")
		raw.WriteString(number)
		raw.WriteString(".pdf\"\r\nContent-Disposition: attachment; filename=\"report-")
		raw.WriteString(number)
		raw.WriteString(".pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		raw.WriteString(payload)
	}
	raw.WriteString("--outer-boundary--\r\n")
	return raw.String()
}

func benchmarkCampaignHTML(itemCount int) string {
	var html strings.Builder
	html.Grow(itemCount * 360)
	html.WriteString(`<html><body><main>`)
	for index := range itemCount {
		number := strconv.Itoa(index)
		scheme := "https://"
		if index%11 == 0 {
			scheme = "http://"
		}
		html.WriteString(`<article><h2>Campaign item `)
		html.WriteString(number)
		html.WriteString(`</h2><p>Current release highlights and operational guidance for subscribers.</p><a href="`)
		html.WriteString(scheme)
		html.WriteString(`example.test/campaign/items/`)
		html.WriteString(number)
		html.WriteString(`"><strong>Read</strong> the full update</a><img src="https://cdn.example.test/campaign/`)
		html.WriteString(number)
		html.WriteString(`.jpg"`)
		if index%7 != 0 {
			html.WriteString(` alt="Campaign illustration"`)
		}
		html.WriteString(`>`)
		if index%16 == 0 {
			html.WriteString(`<img src="https://metrics.example.test/open/`)
			html.WriteString(number)
			html.WriteString(`.gif" width="1" height="1" alt="">`)
		}
		html.WriteString(`</article>`)
	}
	html.WriteString(`</main></body></html>`)
	return html.String()
}

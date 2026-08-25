package mimeparse

import (
	"bytes"
	"encoding/base64"
	"mime/quotedprintable"
	"strings"
	"testing"

	"github.com/emersion/go-message"
)

func TestParseIndexesOrderedHeadersAndNestedRanges(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"From: Sender <sender@example.test>",
		"X-Duplicate: first",
		"X-Duplicate:\tsecond",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"preamble",
		"--outer",
		"Content-Type: text/plain; charset=utf-8",
		"X-Folded: one",
		" two",
		"",
		"body",
		"--outer--",
		"epilogue",
	}, "\r\n"))

	document, err := Parse(raw, InspectionLimits)
	if err != nil {
		t.Fatal(err)
	}
	if document.SemanticError != nil || document.Truncated {
		t.Fatalf("semantic=%v truncated=%v causes=%v", document.SemanticError, document.Truncated, document.TruncationCauses)
	}
	if document.Root == nil || len(document.Root.Children) != 1 {
		t.Fatalf("root = %#v", document.Root)
	}
	if got := []string{document.Root.HeaderFields[1].Name, document.Root.HeaderFields[2].Name}; got[0] != "X-Duplicate" || got[1] != "X-Duplicate" {
		t.Fatalf("duplicate fields = %v", got)
	}
	first := document.Root.HeaderFields[1]
	if string(raw[first.RawField.Start:first.RawField.End]) != "X-Duplicate: first\r\n" || string(raw[first.RawValue.Start:first.RawValue.End]) != " first\r\n" {
		t.Fatalf("ranges = field %q value %q", raw[first.RawField.Start:first.RawField.End], raw[first.RawValue.Start:first.RawValue.End])
	}
	child := document.Root.Children[0]
	if child.Path != "1.1" || string(raw[child.RawBody.Start:child.RawBody.End]) != "body" {
		t.Fatalf("child path/body = %q/%q range=%#v", child.Path, raw[child.RawBody.Start:child.RawBody.End], child.RawBody)
	}
	if len(document.Root.BoundaryDelimiters) != 2 || !document.Root.BoundaryClosed {
		t.Fatalf("boundaries = %#v closed=%v", document.Root.BoundaryDelimiters, document.Root.BoundaryClosed)
	}
	if len(document.Lines) != 15 || document.Lines[len(document.Lines)-1].Terminator != "EOF" {
		t.Fatalf("lines = %#v", document.Lines)
	}
}

func TestParseSelectionDecodingAndFilename(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"--outer",
		"Content-Type: multipart/alternative; boundary=alt",
		"",
		"--alt",
		"Content-Type: text/plain; charset=iso-8859-1",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"caf=E9",
		"--alt",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>chosen</p>",
		"--alt--",
		"--outer",
		"Content-Type: application/octet-stream",
		"Content-Disposition: attachment; filename*=utf-8''caf%C3%A9.txt",
		"Content-Transfer-Encoding: base64",
		"",
		"YXR0YWNobWVudA==",
		"--outer--",
		"",
	}, "\r\n"))
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if document.Presentation.Text == nil || string(document.Presentation.Text.DecodedBody) != "café" {
		t.Fatalf("text = %#v", document.Presentation.Text)
	}
	if document.Presentation.HTML == nil || string(document.Presentation.HTML.DecodedBody) != "<p>chosen</p>" {
		t.Fatalf("html = %#v", document.Presentation.HTML)
	}
	if len(document.Presentation.Attachments) != 1 || Filename(document.Presentation.Attachments[0].Node) != "café.txt" || string(document.Presentation.Attachments[0].Node.DecodedBody) != "attachment" {
		t.Fatalf("attachments = %#v", document.Presentation.Attachments)
	}
}

func TestParseUnknownEncodingAndCharsetRemainIdentifiable(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []byte
		is   func(error) bool
	}{
		{"encoding", []byte("Content-Type: text/html\r\nContent-Transfer-Encoding: x-unknown\r\n\r\n<p>opaque</p>"), message.IsUnknownEncoding},
		{"charset", append([]byte("Content-Type: text/plain; charset=x-unknown\r\n\r\n"), 0xff), message.IsUnknownCharset},
	} {
		t.Run(test.name, func(t *testing.T) {
			document, err := Parse(test.raw, InspectionLimits)
			if err != nil || document.SemanticError != nil || document.Root == nil {
				t.Fatalf("Parse: err=%v semantic=%v root=%v", err, document.SemanticError, document.Root)
			}
			if !test.is(document.Root.DecodeError) {
				t.Fatalf("decode error = %v", document.Root.DecodeError)
			}
			if !bytes.Contains(document.Root.DecodedBody, []byte("opaque")) && len(document.Root.DecodedBody) == 0 {
				t.Fatalf("decoded body = %q", document.Root.DecodedBody)
			}
		})
	}
}

func TestParseMalformedHeadersAndMultipartDigestDefault(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"Content-Type: multipart/digest; boundary=digest",
		"Content-Disposition: attachment; filename",
		"",
		"--digest",
		"From: nested@example.test",
		"",
		"nested body",
		"--digest--",
		"",
	}, "\r\n"))
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if !document.Root.MalformedDisposition {
		t.Fatal("malformed disposition not recorded")
	}
	if len(document.Root.Children) != 1 || document.Root.Children[0].MediaType != "message/rfc822" {
		t.Fatalf("digest child media type = %q", document.Root.Children[0].MediaType)
	}
}

func TestParseMultipartDigestDefaultWithoutRawIndex(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"Content-Type: multipart/digest; boundary=digest",
		"",
		"--digest",
		"From: nested@example.test",
		"",
		"nested body",
		"--digest--",
		"",
	}, "\r\n"))
	document, err := Parse(raw, Limits{})
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if document.Root == nil || len(document.Root.Children) != 1 {
		t.Fatalf("document=%#v", document)
	}
	child := document.Root.Children[0]
	if child.MediaType != "message/rfc822" || len(child.Children) != 1 || child.Children[0].MediaType != "text/plain" {
		t.Fatalf("digest child=%#v", child)
	}
}

func TestParseIndexesEncapsulatedMessagesWithoutSelectingNestedBody(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=outer",
		"",
		"--outer",
		"Content-Type: text/plain",
		"",
		"outer body",
		"--outer",
		"Content-Type: message/rfc822",
		"Content-Disposition: attachment; filename=nested.eml",
		"",
		"From: nested@example.test",
		"Content-Type: multipart/alternative; boundary=alt",
		"",
		"--alt",
		"Content-Type: text/plain",
		"",
		"nested plain",
		"--alt",
		"Content-Type: text/html",
		"",
		"<p>nested html</p>",
		"--alt--",
		"--outer--",
		"",
	}, "\r\n"))
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if document.Root == nil || len(document.Root.Children) != 2 {
		t.Fatalf("root=%#v", document.Root)
	}
	encapsulated := document.Root.Children[1]
	if encapsulated.Path != "1.2" || encapsulated.MediaType != "message/rfc822" || len(encapsulated.Children) != 1 {
		t.Fatalf("encapsulated=%#v", encapsulated)
	}
	nested := encapsulated.Children[0]
	if nested.Path != "1.2.1" || nested.MediaType != "multipart/alternative" || len(nested.Children) != 2 {
		t.Fatalf("nested=%#v", nested)
	}
	if document.Presentation.Text == nil || string(document.Presentation.Text.DecodedBody) != "outer body" {
		t.Fatalf("outer presentation=%#v", document.Presentation)
	}
	if len(document.Presentation.Attachments) != 1 || document.Presentation.Attachments[0].Node != encapsulated {
		t.Fatalf("attachments=%#v", document.Presentation.Attachments)
	}
}

func TestParseMultipartDigestIndexesImplicitEncapsulatedMessage(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"Content-Type: multipart/digest; boundary=digest",
		"",
		"--digest",
		"",
		"From: nested@example.test",
		"Content-Type: text/plain",
		"",
		"nested body",
		"--digest--",
		"",
	}, "\r\n"))
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if document.Root == nil || len(document.Root.Children) != 1 || len(document.Root.Children[0].Children) != 1 {
		t.Fatalf("document=%#v", document)
	}
	encapsulated := document.Root.Children[0]
	if encapsulated.MediaType != "message/rfc822" || encapsulated.Children[0].Path != "1.1.1" || encapsulated.Children[0].MediaType != "text/plain" {
		t.Fatalf("encapsulated=%#v", encapsulated)
	}
}

func TestParseEncapsulatedMessageChargesDepthLimit(t *testing.T) {
	raw := []byte("Content-Type: message/global\r\n\r\nFrom: nested@example.test\r\n\r\nbody")
	document, err := Parse(raw, Limits{MaxRawBytes: len(raw), MaxDepth: 1, MaxParts: 10, MaxHeaderFields: 10, MaxHeaderBytes: 1024, MaxPhysicalLines: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !document.Truncated || len(document.TruncationCauses) != 1 || document.TruncationCauses[0] != "MIME depth" {
		t.Fatalf("truncation=%v causes=%v", document.Truncated, document.TruncationCauses)
	}
}

func TestParseMalformedTransferPayloadIsSemanticFailure(t *testing.T) {
	raw := []byte("Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n!!!!")
	document, err := Parse(raw, InspectionLimits)
	if err != nil {
		t.Fatal(err)
	}
	if document.SemanticError == nil || document.Root == nil || document.Root.DecodeError == nil {
		t.Fatalf("document = %#v", document)
	}
}

func TestParseUnterminatedBoundaryIsSemanticFailureWithIndexedPrefix(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nbody")
	document, err := Parse(raw, InspectionLimits)
	if err != nil {
		t.Fatal(err)
	}
	if document.SemanticError == nil || document.Root == nil || len(document.Root.Children) != 1 || document.Root.BoundaryClosed {
		t.Fatalf("document = %#v", document)
	}
	if document.ParsedThroughPath != nil {
		t.Fatalf("parsed through = %v, want nil because no complete node precedes fatal container completion", document.ParsedThroughPath)
	}
}

func TestParseLimits(t *testing.T) {
	raw := []byte("A: one\r\nB: two\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nbody\r\n--x--\r\n")
	tests := []struct {
		name  string
		limit Limits
		cause string
	}{
		{"raw bytes", Limits{MaxRawBytes: 10}, "raw bytes"},
		{"physical lines", Limits{MaxPhysicalLines: 2}, "physical lines"},
		{"header fields", Limits{MaxHeaderFields: 2}, "header fields"},
		{"header bytes", Limits{MaxHeaderBytes: 10}, "header bytes"},
		{"parts", Limits{MaxParts: 1}, "MIME parts"},
		{"depth", Limits{MaxDepth: 1}, "MIME depth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := Parse(raw, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !document.Truncated || len(document.TruncationCauses) != 1 || document.TruncationCauses[0] != test.cause {
				t.Fatalf("truncation = %v %v", document.Truncated, document.TruncationCauses)
			}
		})
	}
}

func TestParseMalformedContentTypeRemainsSenderError(t *testing.T) {
	raw := []byte("Content-Type: text/html; charset\r\n\r\n<body>")
	document, err := Parse(raw, InspectionLimits)
	if err != nil {
		t.Fatalf("unexpected invariant error: %v", err)
	}
	if document.Root == nil || !document.Root.MalformedContentType {
		t.Fatalf("document=%#v", document)
	}
}

func TestParseSMTPModeRetainsHeaderLimit(t *testing.T) {
	raw := []byte("X-Large: " + strings.Repeat("a", (1<<20)+1) + "\r\n\r\nbody")
	document, err := Parse(raw, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if document.SemanticError == nil {
		t.Fatal("oversized SMTP header was accepted")
	}
}

func TestParseZeroLimitsSkipsIndexes(t *testing.T) {
	document, err := Parse([]byte("Content-Type: text/plain\r\n\r\nbody"), Limits{})
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if len(document.Lines) != 0 || document.Root.RawHeader != (ByteRange{}) || document.Root.RawBody != (ByteRange{}) {
		t.Fatalf("inspection indexes populated: %#v", document)
	}
	if document.Presentation.Text == nil || string(document.Presentation.Text.DecodedBody) != "body" {
		t.Fatalf("presentation = %#v", document.Presentation)
	}
}

func TestParseImplicitDigestWrapperCarriesDecodedAttachment(t *testing.T) {
	nested := "Subject: inner\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\ninner body"
	raw := []byte("Content-Type: multipart/mixed; boundary=mixed\r\n\r\n" +
		"--mixed\r\nContent-Type: text/plain\r\n\r\nintro\r\n" +
		"--mixed\r\nContent-Type: multipart/digest; boundary=digest\r\n\r\n" +
		"--digest\r\nContent-Disposition: attachment; filename=msg.eml\r\n\r\n" +
		nested + "\r\n--digest--\r\n" +
		"--mixed--\r\n")
	for _, limits := range []Limits{{}, InspectionLimits} {
		document, err := Parse(raw, limits)
		if err != nil || document.SemanticError != nil {
			t.Fatalf("limits=%#v: err=%v semantic=%v", limits, err, document.SemanticError)
		}
		if document.Root == nil || len(document.Root.Children) != 2 {
			t.Fatalf("limits=%#v root=%#v", limits, document.Root)
		}
		wrapper := document.Root.Children[1].Children[0]
		if wrapper.MediaType != "message/rfc822" || string(wrapper.DecodedBody) != nested {
			t.Fatalf("limits=%#v wrapper=%#v", limits, wrapper)
		}
		if Filename(wrapper) != "msg.eml" || len(wrapper.Children) != 1 || wrapper.Children[0].Header.Get("Subject") != "inner" {
			t.Fatalf("limits=%#v wrapper metadata=%#v", limits, wrapper)
		}
		if len(document.Presentation.Attachments) != 1 || document.Presentation.Attachments[0].Node != wrapper {
			t.Fatalf("limits=%#v attachments=%#v", limits, document.Presentation.Attachments)
		}
	}
}

func TestParseDecodesBoundaryParametersForRawRouting(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=\"=?UTF-8?Q?outer?=\"\r\n\r\n" +
		"--outer\r\nContent-Type: text/plain\r\n\r\nbody\r\n--outer--\r\n")
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil {
		t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
	}
	if document.Root == nil || len(document.Root.Children) != 1 || len(document.Root.BoundaryDelimiters) != 2 || !document.Root.BoundaryClosed {
		t.Fatalf("root=%#v", document.Root)
	}
	if document.Root.MediaParams["boundary"] != "outer" {
		t.Fatalf("boundary=%q", document.Root.MediaParams["boundary"])
	}
}

func TestParseMarksCappedSemanticPassUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		raw        []byte
		limits     Limits
		wantRoot   bool
		wantSyntax bool
	}{
		{
			name:       "header cap before root",
			raw:        []byte("A: one\r\nB: two\r\nC: three\r\n\r\nbody"),
			limits:     Limits{MaxPhysicalLines: 3},
			wantSyntax: false,
		},
		{
			name:       "uncapped malformed header",
			raw:        []byte("not a header\r\nstill not a header"),
			limits:     Limits{MaxPhysicalLines: 100},
			wantSyntax: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := Parse(test.raw, test.limits)
			if err != nil {
				t.Fatal(err)
			}
			if document.SemanticSkipped != !test.wantSyntax || document.SemanticError != nil != test.wantSyntax {
				t.Fatalf("document=%#v", document)
			}
			if document.Root != nil != test.wantRoot {
				t.Fatalf("root=%#v", document.Root)
			}
		})
	}
}

func TestParseCappedTailPathExcludesUnindexedBody(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nfirst\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\ntail one\r\ntail two\r\n" +
		"--b--\r\n")
	document, err := Parse(raw, Limits{MaxPhysicalLines: 11})
	if err != nil || document.SemanticError != nil || !document.SemanticSkipped {
		t.Fatalf("document: err=%v %#v", err, document)
	}
	if document.ParsedThroughPath == nil || *document.ParsedThroughPath != "1.1" {
		t.Fatalf("parsed through %v", document.ParsedThroughPath)
	}
}

func TestParseTransferEncodedEncapsulatedChildren(t *testing.T) {
	nested := "Subject: inner\r\nContent-Type: text/plain\r\n\r\ninner body"
	tests := []struct {
		name   string
		encode func([]byte) string
	}{
		{
			name: "base64",
			encode: func(value []byte) string {
				return base64.StdEncoding.EncodeToString(value)
			},
		},
		{
			name: "quoted-printable",
			encode: func(value []byte) string {
				var encoded bytes.Buffer
				writer := quotedprintable.NewWriter(&encoded)
				_, _ = writer.Write(value)
				_ = writer.Close()
				return encoded.String()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte("Content-Type: multipart/mixed; boundary=m\r\n\r\n--m\r\n" +
				"Content-Type: message/rfc822\r\nContent-Transfer-Encoding: " + test.name + "\r\n\r\n" +
				test.encode([]byte(nested)) + "\r\n--m--\r\n")
			document, err := Parse(raw, InspectionLimits)
			if err != nil || document.SemanticError != nil {
				t.Fatalf("Parse: err=%v semantic=%v", err, document.SemanticError)
			}
			encapsulated := document.Root.Children[0]
			if string(encapsulated.DecodedBody) != nested || len(encapsulated.Children) != 1 || encapsulated.Children[0].Header.Get("Subject") != "inner" {
				t.Fatalf("encapsulated=%#v", encapsulated)
			}
		})
	}
}

func TestParsePreservesDecodedDispositionParameters(t *testing.T) {
	raw := []byte("Content-Type: multipart/mixed; boundary=m\r\n\r\n--m\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"=?UTF-8?Q?caf=C3=A9.txt?=\"\r\n\r\n" +
		"data\r\n--m--\r\n")
	for _, limits := range []Limits{{}, InspectionLimits} {
		document, err := Parse(raw, limits)
		if err != nil || document.SemanticError != nil {
			t.Fatalf("limits=%#v: err=%v semantic=%v", limits, err, document.SemanticError)
		}
		if got := Filename(document.Root.Children[0]); got != "café.txt" {
			t.Fatalf("limits=%#v filename=%q", limits, got)
		}
	}
}

func TestParseLoneCarriageReturnIsContentNotTerminator(t *testing.T) {
	raw := []byte("Content-Type: text/plain\rX: y\r\n\r\nbody")
	document, err := Parse(raw, InspectionLimits)
	if err != nil || document.SemanticError != nil || document.Root == nil {
		t.Fatalf("indexed Parse: err=%v semantic=%v root=%v", err, document.SemanticError, document.Root)
	}
	if len(document.Lines) != 3 || document.Lines[0].Terminator != "CRLF" {
		t.Fatalf("lines = %#v", document.Lines)
	}
	fields := document.Root.HeaderFields
	if len(fields) != 1 || fields[0].Name != "Content-Type" {
		t.Fatalf("header fields = %#v", fields)
	}
	if !document.Root.MalformedContentType || document.Root.MediaType != "text/plain" {
		t.Fatalf("root media type = %q malformed=%v", document.Root.MediaType, document.Root.MalformedContentType)
	}
	if string(document.Root.DecodedBody) != "body" {
		t.Fatalf("decoded body = %q", document.Root.DecodedBody)
	}
	withoutIndex, err := Parse(raw, Limits{})
	if err != nil || withoutIndex.SemanticError != nil || withoutIndex.Root == nil {
		t.Fatalf("semantic Parse: err=%v semantic=%v root=%v", err, withoutIndex.SemanticError, withoutIndex.Root)
	}
	if !withoutIndex.Root.MalformedContentType {
		t.Fatalf("semantic root malformed content type = %v", withoutIndex.Root.MalformedContentType)
	}
	if string(withoutIndex.Root.DecodedBody) != "body" {
		t.Fatalf("semantic decoded body = %q", withoutIndex.Root.DecodedBody)
	}
}

func TestParseDeepNestingBeyondMaxDepthFailsCleanlyOnBothPaths(t *testing.T) {
	var builder strings.Builder
	for level := range InspectionLimits.MaxDepth + 8 {
		builder.WriteString("Content-Type: multipart/mixed; boundary=b" + itoa(level) + "\r\n\r\n--b" + itoa(level) + "\r\n")
	}
	builder.WriteString("leaf")
	raw := []byte(builder.String())

	indexed, err := Parse(raw, InspectionLimits)
	if err != nil {
		t.Fatalf("indexed Parse: %v", err)
	}
	if !indexed.Truncated || !indexed.SemanticSkipped {
		t.Fatalf("indexed document truncation flags: truncated=%v semanticSkipped=%v causes=%v", indexed.Truncated, indexed.SemanticSkipped, indexed.TruncationCauses)
	}

	withoutIndex, err := Parse(raw, Limits{})
	if err != nil {
		t.Fatalf("semantic Parse: %v", err)
	}
	if withoutIndex.SemanticError == nil {
		t.Fatal("semantic parser did not report maximum nesting depth")
	}
}

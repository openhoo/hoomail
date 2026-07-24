package mimeparse

import (
	"strings"
	"testing"
)

var benchmarkDocument Document

func BenchmarkParse(b *testing.B) {
	fixtures := []struct {
		name   string
		raw    []byte
		limits Limits
	}{
		{"SMTPPlain", benchmarkPlain(4 << 10), Limits{}},
		{"InspectionPlain64KiB", benchmarkPlain(64 << 10), InspectionLimits},
		{"InspectionMultipart128Parts", benchmarkMultipart(128), InspectionLimits},
		{"InspectionCapSaturating", benchmarkMultipart(512), Limits{MaxRawBytes: 1 << 20, MaxDepth: 8, MaxParts: 64, MaxHeaderFields: 256, MaxHeaderBytes: 64 << 10, MaxPhysicalLines: 4096}},
	}
	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.raw)))
			for b.Loop() {
				document, err := Parse(fixture.raw, fixture.limits)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkDocument = document
			}
		})
	}
}

func benchmarkPlain(size int) []byte {
	header := "From: sender@example.test\r\nTo: recipient@example.test\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"
	return []byte(header + strings.Repeat("x", max(0, size-len(header))))
}

func benchmarkMultipart(parts int) []byte {
	var builder strings.Builder
	builder.WriteString("MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=bench\r\n\r\n")
	for index := 0; index < parts; index++ {
		builder.WriteString("--bench\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=part.bin\r\n\r\n")
		builder.WriteString(strings.Repeat("x", 256))
		builder.WriteString("\r\n")
	}
	builder.WriteString("--bench--\r\n")
	return []byte(builder.String())
}

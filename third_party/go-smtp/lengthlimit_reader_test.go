package smtp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLineLimitReaderDoesNotInspectPrefetchedChunk(t *testing.T) {
	command := "BDAT 6000 LAST\r\n"
	chunk := strings.Repeat("x", 6000)
	reader := &lineLimitReader{R: strings.NewReader(command + chunk + "NOOP\r\n"), LineLimit: 2000}
	buffer := make([]byte, 8192)
	n, err := reader.Read(buffer)
	if err != nil || string(buffer[:n]) != command {
		t.Fatalf("command read = %q, %v; want %q", buffer[:n], err, command)
	}
	reader.LineLimit = 0
	got := make([]byte, len(chunk))
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != chunk {
		t.Fatalf("chunk read length = %d, error = %v", len(got), err)
	}
	reader.LineLimit = 2000
	n, err = reader.Read(buffer)
	if err != nil || string(buffer[:n]) != "NOOP\r\n" {
		t.Fatalf("command after chunk = %q, %v", buffer[:n], err)
	}
}

func TestLineLimitReaderRejectsActualOversizeCommand(t *testing.T) {
	reader := &lineLimitReader{R: bytes.NewBufferString(strings.Repeat("x", 2001) + "\r\n"), LineLimit: 2000}
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrTooLongLine) {
		t.Fatalf("error = %v, want ErrTooLongLine", err)
	}
}

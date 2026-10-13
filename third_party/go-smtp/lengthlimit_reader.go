package smtp

import (
	"bufio"
	"errors"
	"io"
)

var ErrTooLongLine = errors.New("smtp: too long a line in input stream")

// lineLimitReader reads from the underlying Reader but restricts
// line length of lines in input stream to a certain length.
//
// If line length exceeds the limit - Read returns ErrTooLongLine
type lineLimitReader struct {
	R         io.Reader
	LineLimit int

	curLineLength int
	reader        *bufio.Reader
}

func (r *lineLimitReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if r.curLineLength > r.LineLimit && r.LineLimit > 0 {
		return 0, ErrTooLongLine
	}

	if r.reader == nil {
		r.reader = bufio.NewReader(r.R)
	}
	if r.LineLimit == 0 {
		// A BDAT chunk is arbitrary bytes, not a partial command line. Keep
		// prefetched bytes available and start a fresh line once limiting is
		// restored by the connection handler.
		r.curLineLength = 0
		return r.reader.Read(b)
	}

	// Stop at the first newline so textproto cannot prefetch and validate
	// bytes belonging to a following BDAT chunk before its command handler
	// disables line limiting. The buffered source retains those bytes.
	for n := 0; n < len(b); n++ {
		chr, err := r.reader.ReadByte()
		if err != nil {
			return n, err
		}
		b[n] = chr
		if chr == '\n' {
			r.curLineLength = 0
			return n + 1, nil
		}
		r.curLineLength++

		if r.curLineLength > r.LineLimit {
			return 0, ErrTooLongLine
		}
	}

	return len(b), nil
}

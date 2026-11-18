package parse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// lineReader yields lines from a JSONL stream without ever holding more than
// maxBytes of a single line in memory. Oversized lines are skipped (reported
// through the tooLong flag) so one huge record cannot exhaust memory.
type lineReader struct {
	r        *bufio.Reader
	maxBytes int
	lineNo   int
	buf      []byte
	first    bool
}

func newLineReader(r io.Reader, maxBytes int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64*1024), maxBytes: maxBytes, first: true}
}

// next returns the next line (without line terminator), its 1-based number,
// whether it exceeded maxBytes (in which case line is nil), and io.EOF at end.
func (lr *lineReader) next() (line []byte, no int, tooLong bool, err error) {
	lr.buf = lr.buf[:0]
	sawAny := false
	for {
		chunk, rerr := lr.r.ReadSlice('\n')
		if len(chunk) > 0 {
			sawAny = true
			if !tooLong {
				if len(lr.buf)+len(chunk) > lr.maxBytes+2 { // +2 tolerates "\r\n"
					tooLong = true
					lr.buf = lr.buf[:0]
				} else {
					lr.buf = append(lr.buf, chunk...)
				}
			}
		}
		if rerr == nil {
			break
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(rerr, io.EOF) {
			if !sawAny {
				return nil, lr.lineNo, false, io.EOF
			}
			break
		}
		return nil, lr.lineNo, false, fmt.Errorf("read: %w", rerr)
	}
	lr.lineNo++
	if tooLong {
		return nil, lr.lineNo, true, nil
	}
	line = bytes.TrimRight(lr.buf, "\r\n")
	if lr.first {
		line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		lr.first = false
	}
	return line, lr.lineNo, false, nil
}

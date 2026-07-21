package plugin

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// readLimitedLine reads one newline-terminated line, refusing to buffer more
// than limit bytes so a runaway plugin cannot exhaust agent memory.
func readLimitedLine(r *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if len(out) > limit {
			return nil, fmt.Errorf("response line exceeds %d bytes", limit)
		}
		if !isPrefix {
			return out, nil
		}
	}
}

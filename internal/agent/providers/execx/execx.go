// Package execx runs external CLIs (systemctl, journalctl, docker compose)
// with output capping and consistent error text.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const outputCap = 8 << 20

// Run executes the command and returns stdout. stderr is folded into the
// error on failure.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, left: outputCap}
	cmd.Stderr = &limitWriter{w: &stderr, left: 64 << 10}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Stream starts the command and returns its stdout pipe; cancel ctx to stop
// it. The returned wait func must be called after the pipe is drained.
func Stream(ctx context.Context, name string, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return pipe, cmd.Wait, nil
}

type limitWriter struct {
	w    io.Writer
	left int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.left <= 0 {
		return len(p), nil
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	n, err := l.w.Write(p)
	l.left -= n
	if err != nil {
		return n, err
	}
	return len(p), nil
}

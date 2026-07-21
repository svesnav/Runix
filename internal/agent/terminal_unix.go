//go:build !windows

package agent

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"

	rt "github.com/runix/runix/internal/domain/runtime"
)

// openHostTerminal starts the login shell on a PTY.
func openHostTerminal(cols, rows uint16) (rt.Terminal, error) {
	shell := resolveShell()
	cmd := exec.Command(shell, shellArgs(shell)...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("agent: start pty: %w", err)
	}
	return &hostTerminal{f: f, cmd: cmd}, nil
}

type hostTerminal struct {
	f   *os.File
	cmd *exec.Cmd
}

func (t *hostTerminal) Read(p []byte) (int, error)  { return t.f.Read(p) }
func (t *hostTerminal) Write(p []byte) (int, error) { return t.f.Write(p) }

func (t *hostTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.f, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *hostTerminal) Close() error {
	_ = t.f.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.cmd.Wait()
	return nil
}

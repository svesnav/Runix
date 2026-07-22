//go:build !windows

package daemon

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setProcAttrs puts the daemon in its own process group so signals reach
// the whole tree.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

var signalNames = map[string]syscall.Signal{
	"SIGHUP":  syscall.SIGHUP,
	"SIGINT":  syscall.SIGINT,
	"SIGQUIT": syscall.SIGQUIT,
	"SIGKILL": syscall.SIGKILL,
	"SIGUSR1": syscall.SIGUSR1,
	"SIGUSR2": syscall.SIGUSR2,
	"SIGTERM": syscall.SIGTERM,
}

// validStopSignal reports whether a spec's stop signal can actually be
// delivered on this platform. normalize() fills the default, so an empty
// name only reaches here on a spec that skipped it.
func validStopSignal(name string) bool {
	if name == "" {
		return true
	}
	_, ok := signalNames[name]
	return ok
}

func signalProcess(cmd *exec.Cmd, name string) error {
	sig, ok := signalNames[name]
	if !ok {
		return fmt.Errorf("daemon: unknown signal %q", name)
	}
	// Negative pid signals the process group.
	return syscall.Kill(-cmd.Process.Pid, sig)
}

func killProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

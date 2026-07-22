//go:build windows

package daemon

import "os/exec"

func setProcAttrs(*exec.Cmd) {}

// Any signal name is accepted so a spec written for a Linux host still
// validates when the agent runs on Windows for development; it simply has
// no effect below.
func validStopSignal(string) bool { return true }

// Windows has no POSIX signals; both paths terminate the process.
func signalProcess(cmd *exec.Cmd, _ string) error {
	return cmd.Process.Kill()
}

func killProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

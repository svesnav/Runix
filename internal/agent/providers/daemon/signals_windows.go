//go:build windows

package daemon

import "os/exec"

func setProcAttrs(*exec.Cmd) {}

// Windows has no POSIX signals; both paths terminate the process.
func signalProcess(cmd *exec.Cmd, _ string) error {
	return cmd.Process.Kill()
}

func killProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

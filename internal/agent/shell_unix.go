//go:build !windows

package agent

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Tried in order when nothing more specific is known. bash first because
// it is what an operator opening a terminal almost always expects; /bin/sh
// is last because on Debian and Ubuntu it is dash, which has no history,
// no completion and a bare "$" prompt.
var fallbackShells = []string{
	"/bin/bash",
	"/usr/bin/bash",
	"/bin/zsh",
	"/usr/bin/zsh",
	"/bin/ash",
	"/bin/sh",
}

// resolveShell picks the shell a host terminal should run.
//
// $SHELL is deliberately not the first choice. The agent normally runs as a
// systemd service, whose environment has no $SHELL at all — so trusting it
// silently gave everyone /bin/sh even on hosts where the account's shell is
// bash. The account's own entry in /etc/passwd is the authoritative answer;
// $SHELL only helps when the agent was started from an interactive session.
func resolveShell() string {
	if s := os.Getenv("RUNIX_AGENT_SHELL"); usableShell(s) {
		return s
	}
	if s := passwdShell(os.Getuid()); usableShell(s) {
		return s
	}
	if s := os.Getenv("SHELL"); usableShell(s) {
		return s
	}
	for _, s := range fallbackShells {
		if usableShell(s) {
			return s
		}
	}
	return "/bin/sh"
}

func usableShell(path string) bool {
	if path == "" || path == "/usr/sbin/nologin" || path == "/sbin/nologin" || path == "/bin/false" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// passwdShell reads the login shell for a uid straight from /etc/passwd.
// os/user cannot help here: its User struct carries no shell field, and the
// cgo-free build resolves entries without it anyway.
func passwdShell(uid int) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()

	want := strconv.Itoa(uid)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// name:passwd:uid:gid:gecos:home:shell
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[2] != want {
			continue
		}
		return strings.TrimSpace(fields[6])
	}
	return ""
}

// shellArgs returns the arguments that make the shell interactive and
// login-like. Not every shell understands -l, so it is only passed to the
// ones that do.
func shellArgs(shell string) []string {
	switch base(shell) {
	case "bash", "zsh", "sh", "ash", "dash", "ksh":
		return []string{"-l"}
	default:
		// An unfamiliar shell (fish, nushell, …) is started plain rather
		// than with a flag it may reject.
		return nil
	}
}

func base(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

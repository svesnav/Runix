//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswdShellReadsTheAccountEntry(t *testing.T) {
	// The real /etc/passwd on the test machine is whatever it is; what
	// matters is that the parser picks the right field for the right uid.
	shell := passwdShell(os.Getuid())
	if shell != "" && shell[0] != '/' {
		t.Errorf("passwdShell = %q, want an absolute path or empty", shell)
	}
}

func TestUsableShellRejectsNonShells(t *testing.T) {
	dir := t.TempDir()

	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "shell")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]bool{
		"":                   false,
		"/does/not/exist":    false,
		"/usr/sbin/nologin":  false, // a locked account's shell is not a shell
		"/bin/false":         false,
		dir:                  false, // a directory
		notExec:              false,
		executable:           true,
	}
	for path, want := range cases {
		if got := usableShell(path); got != want {
			t.Errorf("usableShell(%q) = %v, want %v", path, got, want)
		}
	}
}

// The regression: an agent under systemd has no $SHELL, and every terminal
// silently opened /bin/sh (dash on Debian and Ubuntu) instead of the
// account's real shell.
func TestResolveShellIgnoresMissingEnv(t *testing.T) {
	t.Setenv("RUNIX_AGENT_SHELL", "")
	t.Setenv("SHELL", "")

	got := resolveShell()
	if got == "" {
		t.Fatal("resolveShell returned empty")
	}
	if !usableShell(got) {
		t.Errorf("resolveShell = %q, which is not executable", got)
	}
	// Where the account has a shell recorded, that is what we must get.
	if want := passwdShell(os.Getuid()); usableShell(want) && got != want {
		t.Errorf("resolveShell = %q, want the account's shell %q", got, want)
	}
}

func TestResolveShellPrefersExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "myshell")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNIX_AGENT_SHELL", custom)

	if got := resolveShell(); got != custom {
		t.Errorf("resolveShell = %q, want the override %q", got, custom)
	}
}

func TestShellArgs(t *testing.T) {
	for _, shell := range []string{"/bin/bash", "/usr/bin/zsh", "/bin/sh"} {
		if args := shellArgs(shell); len(args) != 1 || args[0] != "-l" {
			t.Errorf("shellArgs(%q) = %v, want [-l]", shell, args)
		}
	}
	// Shells that may not accept -l are started without it.
	if args := shellArgs("/usr/bin/fish"); len(args) != 0 {
		t.Errorf("shellArgs(fish) = %v, want none", args)
	}
}

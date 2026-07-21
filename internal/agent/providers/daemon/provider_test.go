package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"runtime"
	"testing"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// shortCmd returns a command that prints one line and exits 0.
func shortCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo hello-from-daemon"}
	}
	return []string{"/bin/sh", "-c", "echo hello-from-daemon"}
}

// sleepCmd returns a long-running command.
func sleepCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "ping -n 60 127.0.0.1 > NUL"}
	}
	return []string{"/bin/sh", "-c", "sleep 60"}
}

// failCmd exits non-zero immediately.
func failCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "exit 3"}
	}
	return []string{"/bin/sh", "-c", "exit 3"}
}

func requireShell(t *testing.T) {
	t.Helper()
	name := "/bin/sh"
	if runtime.GOOS == "windows" {
		name = "cmd"
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("shell %s not available", name)
	}
}

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := NewProvider(t.TempDir(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.StopAll)
	return p
}

func createDaemon(t *testing.T, p *Provider, name string, cmd []string, policy string, maxRestarts int) rt.Runtime {
	t.Helper()
	cfg, _ := json.Marshal(Spec{
		Cmd: cmd, RestartPolicy: policy, MaxRestarts: maxRestarts,
		RestartDelaySecs: 1, StopTimeoutSecs: 2,
	})
	instance, err := p.Create(context.Background(), rt.Spec{
		Name: name, Type: rt.TypeDaemon, Config: cfg,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return instance
}

func waitState(t *testing.T, instance rt.Runtime, want rt.State, timeout time.Duration) rt.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last rt.Status
	for time.Now().Before(deadline) {
		last, _ = instance.Status(context.Background())
		if last.State == want {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("state = %s, want %s (status %+v)", last.State, want, last)
	return last
}

func TestDaemonRunsAndExitsClean(t *testing.T) {
	requireShell(t)
	p := newTestProvider(t)
	instance := createDaemon(t, p, "echo", shortCmd(), RestartOnFailure, 0)

	if err := instance.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status := waitState(t, instance, rt.StateStopped, 10*time.Second)
	if status.ExitCode == nil || *status.ExitCode != 0 {
		t.Errorf("exit code = %v, want 0", status.ExitCode)
	}

	logs, err := instance.(rt.LogStreamer).Logs(context.Background(), rt.LogOptions{Tail: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	found := false
	for {
		entry, err := logs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(entry.Line) == "hello-from-daemon" {
			found = true
		}
	}
	if !found {
		t.Error("stdout line not captured in logs")
	}
}

func TestDaemonRestartOnFailureGivesUp(t *testing.T) {
	requireShell(t)
	p := newTestProvider(t)
	instance := createDaemon(t, p, "crasher", failCmd(), RestartOnFailure, 2)

	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := waitState(t, instance, rt.StateFailed, 20*time.Second)
	if status.RestartCount < 2 {
		t.Errorf("restart count = %d, want >= 2", status.RestartCount)
	}
	if status.ExitCode == nil || *status.ExitCode != 3 {
		t.Errorf("exit code = %v, want 3", status.ExitCode)
	}
}

func TestDaemonStopLongRunning(t *testing.T) {
	requireShell(t)
	p := newTestProvider(t)
	instance := createDaemon(t, p, "sleeper", sleepCmd(), RestartAlways, 0)

	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitState(t, instance, rt.StateRunning, 10*time.Second)

	if err := instance.Stop(context.Background(), rt.StopOptions{Timeout: 3 * time.Second}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	status := waitState(t, instance, rt.StateStopped, 10*time.Second)
	if status.State != rt.StateStopped {
		t.Errorf("state after stop = %s", status.State)
	}
	// RestartAlways must not resurrect a manually stopped daemon.
	time.Sleep(1500 * time.Millisecond)
	status, _ = instance.Status(context.Background())
	if status.State != rt.StateStopped {
		t.Errorf("daemon resurrected after manual stop: %s", status.State)
	}
}

func TestDaemonPersistsAcrossProviderReload(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	p1, err := NewProvider(dir, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	createDaemon(t, p1, "persisted", shortCmd(), RestartNever, 0)
	p1.StopAll()

	p2, err := NewProvider(dir, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer p2.StopAll()
	descriptors, err := p2.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors[0].Name != "persisted" {
		t.Errorf("descriptors after reload = %+v", descriptors)
	}
}

func TestCreateValidation(t *testing.T) {
	p := newTestProvider(t)
	badSpecs := []Spec{
		{Cmd: []string{}},
		{Cmd: []string{"x"}, RestartPolicy: "sometimes"},
	}
	for i, spec := range badSpecs {
		cfg, _ := json.Marshal(spec)
		if _, err := p.Create(context.Background(), rt.Spec{
			Name: "bad", Type: rt.TypeDaemon, Config: cfg,
		}); !errors.Is(err, rt.ErrInvalidSpec) {
			t.Errorf("spec %d: err = %v, want ErrInvalidSpec", i, err)
		}
	}
	if _, err := p.Create(context.Background(), rt.Spec{
		Name: "bad name!", Type: rt.TypeDaemon,
		Config: json.RawMessage(`{"cmd":["x"]}`),
	}); !errors.Is(err, rt.ErrInvalidSpec) {
		t.Errorf("bad name accepted: %v", err)
	}
}

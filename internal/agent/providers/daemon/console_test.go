package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

// The console tests need a child process that reads stdin and answers on
// stdout. Shell one-liners differ too much between platforms to be
// trustworthy here, so the test binary re-executes itself in a helper mode
// — the child is then identical everywhere.
const echoLoopEnv = "RUNIX_TEST_ECHO_LOOP"

func TestMain(m *testing.M) {
	if os.Getenv(echoLoopEnv) == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println("got:" + scanner.Text())
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// createEchoLoop starts a daemon running the helper above, standing in for
// console-driven software such as a game server.
func createEchoLoop(t *testing.T, p *Provider, name string) rt.Runtime {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	cfg, _ := json.Marshal(Spec{
		Cmd:             []string{self, "-test.run", "TestMain"},
		Env:             map[string]string{echoLoopEnv: "1"},
		RestartPolicy:   RestartNever,
		StopTimeoutSecs: 2,
	})
	instance, err := p.Create(context.Background(), rt.Spec{
		Name: name, Type: rt.TypeDaemon, Config: cfg,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitState(t, instance, rt.StateRunning, 10*time.Second)
	return instance
}

// TestDaemonConsoleRoundTrip is the guarantee the console UI rests on:
// bytes written to the console reach the process's stdin, and the process's
// reply comes back out. Without this, an operator typing "stop" into a
// game server's console would see nothing happen.
func TestDaemonConsoleRoundTrip(t *testing.T) {
	p := newTestProvider(t)
	instance := createEchoLoop(t, p, "echoloop")

	provider, ok := instance.(rt.ConsoleProvider)
	if !ok {
		t.Fatal("daemon runtime does not implement ConsoleProvider")
	}
	console, err := provider.Console(context.Background())
	if err != nil {
		t.Fatalf("Console: %v", err)
	}
	defer console.Close()

	if _, err := console.Write([]byte("hello-stdin\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The reply may be preceded by other output, so scan until it appears.
	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(console)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "got:hello-stdin") {
				found <- scanner.Text()
				return
			}
		}
	}()

	select {
	case line := <-found:
		t.Logf("process echoed: %q", line)
	case <-time.After(15 * time.Second):
		t.Fatal("process never echoed the input written to its console")
	}
}

// TestDaemonConsoleRejectsStoppedProcess keeps the failure legible: writing
// to a dead process must report why rather than silently discarding input.
func TestDaemonConsoleRejectsStoppedProcess(t *testing.T) {
	p := newTestProvider(t)
	instance := createEchoLoop(t, p, "echoloop-stop")

	console, err := instance.(rt.ConsoleProvider).Console(context.Background())
	if err != nil {
		t.Fatalf("Console: %v", err)
	}
	defer console.Close()

	if err := instance.Stop(context.Background(), rt.StopOptions{}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitState(t, instance, rt.StateStopped, 15*time.Second)

	if _, err := console.Write([]byte("too late\n")); !errors.Is(err, rt.ErrInvalidTransition) {
		t.Errorf("Write after stop = %v, want ErrInvalidTransition", err)
	}
}

// TestDaemonAdvertisesConsole guards the capability wiring: the UI decides
// whether to show an input box from this bit alone.
func TestDaemonAdvertisesConsole(t *testing.T) {
	requireShell(t)
	p := newTestProvider(t)
	instance := createDaemon(t, p, "caps", sleepCmd(), RestartNever, 0)

	if !rt.CapabilitiesOf(instance).Has(rt.CapConsole) {
		t.Error("daemon runtime does not advertise CapConsole")
	}
	if !p.Capabilities().Has(rt.CapConsole) {
		t.Error("daemon provider does not advertise CapConsole")
	}
}

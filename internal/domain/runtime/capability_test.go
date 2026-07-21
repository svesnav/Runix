package runtime

import (
	"context"
	"reflect"
	"testing"
)

type baseRuntime struct{}

func (baseRuntime) ID() ID     { return "r1" }
func (baseRuntime) Type() Type { return TypeDaemon }
func (baseRuntime) Status(context.Context) (Status, error) {
	return Status{State: StateRunning}, nil
}
func (baseRuntime) Start(context.Context) error                { return nil }
func (baseRuntime) Stop(context.Context, StopOptions) error    { return nil }
func (baseRuntime) Restart(context.Context, StopOptions) error { return nil }

type loggingRuntime struct{ baseRuntime }

func (loggingRuntime) Logs(context.Context, LogOptions) (LogStream, error) {
	return nil, ErrNotSupported
}

func (loggingRuntime) Pause(context.Context) error  { return nil }
func (loggingRuntime) Resume(context.Context) error { return nil }

func TestCapabilitySet(t *testing.T) {
	s := NewCapabilitySet(CapLogs, CapExec)
	if !s.Has(CapLogs) || !s.Has(CapExec) {
		t.Fatal("set is missing capabilities it was built with")
	}
	if s.Has(CapPause) {
		t.Fatal("set reports a capability it does not have")
	}
	s = s.With(CapPause)
	if !s.Has(CapPause) {
		t.Fatal("With did not add capability")
	}
	want := []string{"exec", "logs", "pause"}
	if got := s.Strings(); !reflect.DeepEqual(got, want) {
		t.Errorf("Strings() = %v, want %v", got, want)
	}
}

func TestCapabilityString(t *testing.T) {
	if got := CapTerminal.String(); got != "terminal" {
		t.Errorf("CapTerminal.String() = %q, want %q", got, "terminal")
	}
	if got := Capability(1 << 60).String(); got != "unknown" {
		t.Errorf("unknown capability String() = %q, want %q", got, "unknown")
	}
}

func TestCapabilitiesOf(t *testing.T) {
	base := CapabilitiesOf(baseRuntime{})
	for _, c := range []Capability{CapStart, CapStop, CapRestart} {
		if !base.Has(c) {
			t.Errorf("base runtime should have %s", c)
		}
	}
	if base.Has(CapLogs) || base.Has(CapPause) {
		t.Error("base runtime reports optional capabilities it lacks")
	}

	full := CapabilitiesOf(loggingRuntime{})
	if !full.Has(CapLogs) || !full.Has(CapPause) {
		t.Error("optional interfaces were not detected")
	}
	if full.Has(CapExec) {
		t.Error("exec capability reported without an Execer implementation")
	}
}

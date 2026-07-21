package runtime

import "testing"

func TestStateTransitions(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		{StateCreated, StateStarting, true},
		{StateStarting, StateRunning, true},
		{StateRunning, StateStopping, true},
		{StateRunning, StateStopped, true}, // process exited on its own
		{StateRunning, StateDegraded, true},
		{StateDegraded, StateRunning, true},
		{StateStopping, StateStopped, true},
		{StateStopped, StateStarting, true},
		{StateRunning, StatePaused, true},
		{StatePaused, StateRunning, true},
		{StateFailed, StateStarting, true},

		{StateCreated, StateRunning, false},
		{StateStopped, StatePaused, false},
		{StatePaused, StateCreated, false},
		{StateStopped, StateStopped, false},
	}
	for _, c := range cases {
		if got := c.from.CanTransitionTo(c.to); got != c.ok {
			t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestStateUnknownIsWildcard(t *testing.T) {
	for _, s := range []State{StateCreated, StateRunning, StateStopped, StateFailed} {
		if !s.CanTransitionTo(StateUnknown) {
			t.Errorf("%s -> unknown should be allowed", s)
		}
		if !StateUnknown.CanTransitionTo(s) {
			t.Errorf("unknown -> %s should be allowed", s)
		}
	}
	if StateUnknown.CanTransitionTo(StateUnknown) {
		t.Error("unknown -> unknown should not count as a transition")
	}
}

func TestStateIsActive(t *testing.T) {
	active := []State{StateStarting, StateRunning, StateDegraded, StatePaused, StateStopping}
	for _, s := range active {
		if !s.IsActive() {
			t.Errorf("%s should be active", s)
		}
	}
	inactive := []State{StateCreated, StateStopped, StateFailed, StateUnknown}
	for _, s := range inactive {
		if s.IsActive() {
			t.Errorf("%s should not be active", s)
		}
	}
}

func TestStateValidate(t *testing.T) {
	if err := StateRunning.Validate(); err != nil {
		t.Errorf("valid state rejected: %v", err)
	}
	if err := State("sleeping").Validate(); err == nil {
		t.Error("invalid state accepted")
	}
}

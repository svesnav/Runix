package runtime

import (
	"fmt"
	"time"
)

// State is the provider-independent lifecycle state every runtime instance
// maps into. Providers translate their native states (Docker container
// states, systemd unit states, process states) into these.
type State string

const (
	StateCreated  State = "created"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateDegraded State = "degraded"
	StatePaused   State = "paused"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
	StateUnknown  State = "unknown"
)

func (s State) Validate() error {
	switch s {
	case StateCreated, StateStarting, StateRunning, StateDegraded, StatePaused,
		StateStopping, StateStopped, StateFailed, StateUnknown:
		return nil
	}
	return fmt.Errorf("%w: unknown state %q", ErrInvalidSpec, s)
}

// IsActive reports whether the instance currently occupies host resources.
func (s State) IsActive() bool {
	switch s {
	case StateStarting, StateRunning, StateDegraded, StatePaused, StateStopping:
		return true
	}
	return false
}

var stateTransitions = map[State]map[State]struct{}{
	StateCreated:  set(StateStarting, StateFailed),
	StateStarting: set(StateRunning, StateDegraded, StateStopping, StateFailed),
	StateRunning:  set(StateDegraded, StatePaused, StateStopping, StateStopped, StateFailed),
	StateDegraded: set(StateRunning, StatePaused, StateStopping, StateStopped, StateFailed),
	StatePaused:   set(StateRunning, StateStopping, StateFailed),
	StateStopping: set(StateStopped, StateFailed),
	StateStopped:  set(StateStarting),
	StateFailed:   set(StateStarting),
}

func set(states ...State) map[State]struct{} {
	m := make(map[State]struct{}, len(states))
	for _, s := range states {
		m[s] = struct{}{}
	}
	return m
}

// CanTransitionTo reports whether the transition is legal. StateUnknown is a
// wildcard in both directions: contact with an instance can be lost from any
// state, and reconciliation can discover any state afterwards.
func (s State) CanTransitionTo(target State) bool {
	if s == StateUnknown || target == StateUnknown {
		return s != target
	}
	_, ok := stateTransitions[s][target]
	return ok
}

// Health is the application-level condition of a runtime instance, distinct
// from its lifecycle state: a running instance can still be unhealthy.
type Health string

const (
	HealthUnknown   Health = "unknown"
	HealthStarting  Health = "starting"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Status is the full observed condition of a runtime instance.
type Status struct {
	State        State      `json:"state"`
	Health       Health     `json:"health"`
	Message      string     `json:"message,omitempty"`
	ExitCode     *int       `json:"exitCode,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	RestartCount int        `json:"restartCount"`
}

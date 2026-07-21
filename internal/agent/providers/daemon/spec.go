// Package daemon is the Runix-native process supervisor: it runs arbitrary
// commands as managed long-lived daemons with restart policies, backoff,
// log capture and resource tracking — independent from systemd, in the
// spirit of PM2/supervisord.
package daemon

import (
	"fmt"
	"regexp"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

const (
	RestartNever     = "never"
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Spec is the persisted daemon definition; it is the provider-specific
// Config document of runtime.Spec.
type Spec struct {
	Name               string            `json:"name"`
	Cmd                []string          `json:"cmd"`
	WorkingDir         string            `json:"workingDir,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	AutoStart          bool              `json:"autoStart"`
	RestartPolicy      string            `json:"restartPolicy"`
	MaxRestarts        int               `json:"maxRestarts"`
	RestartDelaySecs   int               `json:"restartDelaySeconds"`
	MaxRestartDelaySec int               `json:"maxRestartDelaySeconds"`
	StopSignal         string            `json:"stopSignal,omitempty"`
	StopTimeoutSecs    int               `json:"stopTimeoutSeconds"`
}

func (s *Spec) normalize() {
	if s.RestartPolicy == "" {
		s.RestartPolicy = RestartOnFailure
	}
	if s.RestartDelaySecs <= 0 {
		s.RestartDelaySecs = 1
	}
	if s.MaxRestartDelaySec <= 0 {
		s.MaxRestartDelaySec = 60
	}
	if s.StopTimeoutSecs <= 0 {
		s.StopTimeoutSecs = 10
	}
	if s.StopSignal == "" {
		s.StopSignal = "SIGTERM"
	}
}

func (s *Spec) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("%w: daemon name must be 1-64 chars (letters, digits, . _ -)", rt.ErrInvalidSpec)
	}
	if len(s.Cmd) == 0 || s.Cmd[0] == "" {
		return fmt.Errorf("%w: cmd is required", rt.ErrInvalidSpec)
	}
	switch s.RestartPolicy {
	case RestartNever, RestartOnFailure, RestartAlways:
	default:
		return fmt.Errorf("%w: restart policy must be never, on-failure or always", rt.ErrInvalidSpec)
	}
	if s.RestartDelaySecs > 3600 || s.MaxRestartDelaySec > 3600 {
		return fmt.Errorf("%w: restart delays must be at most 1h", rt.ErrInvalidSpec)
	}
	return nil
}

func (s *Spec) restartDelay() time.Duration {
	return time.Duration(s.RestartDelaySecs) * time.Second
}

func (s *Spec) maxRestartDelay() time.Duration {
	return time.Duration(s.MaxRestartDelaySec) * time.Second
}

func (s *Spec) stopTimeout() time.Duration {
	return time.Duration(s.StopTimeoutSecs) * time.Second
}

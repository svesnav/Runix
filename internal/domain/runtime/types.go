package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Type identifies a runtime provider family (docker, systemd, ...).
// New types may be introduced by plugins, so validation checks shape,
// not membership in a fixed list.
type Type string

const (
	TypeDocker  Type = "docker"
	TypeCompose Type = "compose"
	TypeSystemd Type = "systemd"
	TypeDaemon  Type = "daemon"
)

var typePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func (t Type) Validate() error {
	if !typePattern.MatchString(string(t)) {
		return fmt.Errorf("%w: type %q must match %s", ErrInvalidSpec, t, typePattern)
	}
	return nil
}

// ID uniquely identifies a runtime instance within one provider on one
// server (a container ID, a unit name, a daemon UUID).
type ID string

func (id ID) Validate() error {
	s := string(id)
	if strings.TrimSpace(s) == "" || len(s) > 256 {
		return fmt.Errorf("%w: id must be 1-256 non-blank characters", ErrInvalidSpec)
	}
	return nil
}

// Ref addresses a runtime instance anywhere in the fleet.
type Ref struct {
	ServerID string `json:"serverId"`
	Type     Type   `json:"type"`
	ID       ID     `json:"id"`
}

func (r Ref) String() string {
	return fmt.Sprintf("%s/%s/%s", r.ServerID, r.Type, r.ID)
}

// Spec describes a runtime instance to be created. Config carries the
// provider-specific document; each provider unmarshals and validates it
// against its own typed configuration.
type Spec struct {
	Name   string            `json:"name"`
	Type   Type              `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
	Config json.RawMessage   `json:"config,omitempty"`
}

func (s Spec) Validate() error {
	if err := s.Type.Validate(); err != nil {
		return err
	}
	name := strings.TrimSpace(s.Name)
	if name == "" || len(name) > 128 {
		return fmt.Errorf("%w: name must be 1-128 non-blank characters", ErrInvalidSpec)
	}
	return nil
}

// Descriptor is the provider-independent summary of a runtime instance,
// suitable for lists and dashboards.
type Descriptor struct {
	ID        ID                `json:"id"`
	Type      Type              `json:"type"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	Status    Status            `json:"status"`
}

// StopOptions tunes a stop or restart. Zero values mean provider defaults.
type StopOptions struct {
	// Timeout is the grace period before the provider force-kills.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Signal is the POSIX signal name sent first (e.g. "SIGTERM").
	Signal string `json:"signal,omitempty"`
}

// RemoveOptions tunes deletion of a runtime instance.
type RemoveOptions struct {
	// Force removes the instance even if it is running.
	Force bool `json:"force,omitempty"`
	// Purge also removes persistent data owned by the instance
	// (volumes for containers, state directories for daemons).
	Purge bool `json:"purge,omitempty"`
}

// LogOptions selects which log entries a LogStream yields.
type LogOptions struct {
	Follow     bool      `json:"follow,omitempty"`
	Tail       int       `json:"tail,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	Timestamps bool      `json:"timestamps,omitempty"`
}

type LogSource string

const (
	LogSourceStdout LogSource = "stdout"
	LogSourceStderr LogSource = "stderr"
	LogSourceSystem LogSource = "system"
)

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    LogSource `json:"source"`
	Line      []byte    `json:"line"`
}

// LogStream yields log entries. Next returns io.EOF when a non-follow
// stream is exhausted or a follow stream is closed by the provider.
type LogStream interface {
	Next(ctx context.Context) (LogEntry, error)
	Close() error
}

// Metrics is a point-in-time resource snapshot for one runtime instance.
type Metrics struct {
	CollectedAt time.Time      `json:"collectedAt"`
	CPU         CPUMetrics     `json:"cpu"`
	Memory      MemoryMetrics  `json:"memory"`
	Network     NetworkMetrics `json:"network"`
	BlockIO     BlockIOMetrics `json:"blockIo"`
	PIDs        uint64         `json:"pids"`
}

type CPUMetrics struct {
	UsagePercent     float64 `json:"usagePercent"`
	ThrottledPeriods uint64  `json:"throttledPeriods"`
}

type MemoryMetrics struct {
	UsageBytes uint64 `json:"usageBytes"`
	LimitBytes uint64 `json:"limitBytes"`
}

type NetworkMetrics struct {
	RxBytes uint64 `json:"rxBytes"`
	TxBytes uint64 `json:"txBytes"`
}

type BlockIOMetrics struct {
	ReadBytes  uint64 `json:"readBytes"`
	WriteBytes uint64 `json:"writeBytes"`
}

// ExecSpec describes a one-shot command executed inside a runtime instance.
type ExecSpec struct {
	Cmd        []string
	Env        []string
	WorkingDir string
	User       string
	TTY        bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type ExecResult struct {
	ExitCode int `json:"exitCode"`
}

// TerminalSpec describes an interactive session inside a runtime instance.
type TerminalSpec struct {
	// Cmd overrides the provider's default shell when non-empty.
	Cmd  []string
	Env  []string
	User string
	Cols uint16
	Rows uint16
}

// Terminal is a live interactive session: reads yield output, writes send
// input, Resize propagates window size changes.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}

// Availability reports whether a provider is usable on the host it runs on
// (Docker socket reachable, systemd present, ...).
type Availability struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message,omitempty"`
}

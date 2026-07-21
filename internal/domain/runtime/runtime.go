package runtime

import (
	"context"
	"encoding/json"
	"io"
)

// Runtime is the minimal contract every managed workload satisfies,
// regardless of the technology behind it. Operations beyond this core are
// optional capabilities: implementations opt in by additionally satisfying
// the narrow interfaces below, and callers discover support through
// CapabilitiesOf instead of calling methods that can only fail.
type Runtime interface {
	ID() ID
	Type() Type
	Status(ctx context.Context) (Status, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context, opts StopOptions) error
	Restart(ctx context.Context, opts StopOptions) error
}

// Pauser suspends and resumes execution without releasing resources.
type Pauser interface {
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
}

// Reloader applies configuration changes without a restart.
type Reloader interface {
	Reload(ctx context.Context) error
}

// Killer terminates immediately with an explicit POSIX signal name.
type Killer interface {
	Kill(ctx context.Context, signal string) error
}

// LogStreamer exposes historical and live logs.
type LogStreamer interface {
	Logs(ctx context.Context, opts LogOptions) (LogStream, error)
}

// MetricsProvider exposes point-in-time resource usage.
type MetricsProvider interface {
	Metrics(ctx context.Context) (Metrics, error)
}

// Execer runs one-shot commands inside the instance.
type Execer interface {
	Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)
}

// TerminalProvider opens interactive sessions inside the instance.
type TerminalProvider interface {
	Terminal(ctx context.Context, spec TerminalSpec) (Terminal, error)
}

// Console is the *main process's* standard streams: reads yield its output,
// writes go to its stdin. This is what a game server or any console-driven
// daemon expects — distinct from a Terminal, which starts a new shell
// beside the process and cannot talk to it.
type Console interface {
	io.ReadWriteCloser
}

// ConsoleProvider is implemented by runtimes whose main process can accept
// input. Implementations return ErrNotSupported when the instance was not
// started with an input stream (a container created without stdin open).
type ConsoleProvider interface {
	Console(ctx context.Context) (Console, error)
}

// HealthChecker actively probes application-level health.
type HealthChecker interface {
	CheckHealth(ctx context.Context) (Health, error)
}

// Inspector returns the provider-native detail document (the equivalent of
// docker inspect / systemctl show), for power users and debugging.
type Inspector interface {
	Inspect(ctx context.Context) (json.RawMessage, error)
}

// ActionInvoker handles provider-specific actions beyond the universal
// lifecycle (systemd: enable, disable, mask, unmask). Implementations
// return ErrNotSupported for unknown actions.
type ActionInvoker interface {
	InvokeAction(ctx context.Context, action string) error
}

// Updater applies a new configuration document to an existing instance
// in-place, without changing its identity. The document is the same typed
// envelope Create accepts (Spec.Config). Runtimes whose backing technology
// cannot be reconfigured in place (containers) do not implement it.
type Updater interface {
	Update(ctx context.Context, spec Spec) error
}

// Provider manages the population of runtime instances of one Type on one
// host. Adding a new runtime technology to Runix means implementing this
// interface and registering it; nothing above the registry changes.
type Provider interface {
	Type() Type

	// Capabilities declares everything instances of this provider can
	// support, including provider-level operations (create, remove).
	Capabilities() CapabilitySet

	// Availability reports whether the underlying technology is usable on
	// this host. Unavailable providers stay registered so the API can
	// explain why, rather than the type simply vanishing.
	Availability(ctx context.Context) Availability

	List(ctx context.Context) ([]Descriptor, error)
	Get(ctx context.Context, id ID) (Runtime, error)
	Create(ctx context.Context, spec Spec) (Runtime, error)
	Remove(ctx context.Context, id ID, opts RemoveOptions) error
}

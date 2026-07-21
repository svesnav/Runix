package runtime

import "sort"

// Capability names one operation a runtime provider or instance may support.
type Capability uint64

const (
	CapCreate Capability = 1 << iota
	CapRemove
	CapStart
	CapStop
	CapRestart
	CapReload
	CapPause
	CapKill
	CapLogs
	CapMetrics
	CapExec
	CapTerminal
	// CapFiles is declared ahead of the filesystem module; its interface
	// lives there because file access spans more than runtimes.
	CapFiles
	CapHealth
	CapInspect
	CapUpdate
	// CapConsole means the main process accepts input on stdin.
	CapConsole
)

var capabilityNames = map[Capability]string{
	CapCreate:   "create",
	CapRemove:   "remove",
	CapStart:    "start",
	CapStop:     "stop",
	CapRestart:  "restart",
	CapReload:   "reload",
	CapPause:    "pause",
	CapKill:     "kill",
	CapLogs:     "logs",
	CapMetrics:  "metrics",
	CapExec:     "exec",
	CapTerminal: "terminal",
	CapFiles:    "files",
	CapHealth:   "health",
	CapInspect:  "inspect",
	CapUpdate:   "update",
	CapConsole:  "console",
}

func (c Capability) String() string {
	if n, ok := capabilityNames[c]; ok {
		return n
	}
	return "unknown"
}

// CapabilitySet is a bitmask of capabilities, cheap to store and to send
// over the wire so clients can render only the actions a runtime supports.
type CapabilitySet uint64

func NewCapabilitySet(caps ...Capability) CapabilitySet {
	var s CapabilitySet
	return s.With(caps...)
}

func (s CapabilitySet) With(caps ...Capability) CapabilitySet {
	for _, c := range caps {
		s |= CapabilitySet(c)
	}
	return s
}

func (s CapabilitySet) Has(c Capability) bool {
	return s&CapabilitySet(c) != 0
}

// Strings returns the sorted human-readable names of the set, for API
// responses and logs.
func (s CapabilitySet) Strings() []string {
	out := make([]string, 0, len(capabilityNames))
	for c, n := range capabilityNames {
		if s.Has(c) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// CapabilitiesOf derives the instance-level capabilities of rt from the
// optional interfaces it implements. Provider-level capabilities (create,
// remove) are declared by the Provider, not inferred here.
func CapabilitiesOf(rt Runtime) CapabilitySet {
	s := NewCapabilitySet(CapStart, CapStop, CapRestart)
	if _, ok := rt.(Pauser); ok {
		s = s.With(CapPause)
	}
	if _, ok := rt.(Reloader); ok {
		s = s.With(CapReload)
	}
	if _, ok := rt.(Killer); ok {
		s = s.With(CapKill)
	}
	if _, ok := rt.(LogStreamer); ok {
		s = s.With(CapLogs)
	}
	if _, ok := rt.(MetricsProvider); ok {
		s = s.With(CapMetrics)
	}
	if _, ok := rt.(Execer); ok {
		s = s.With(CapExec)
	}
	if _, ok := rt.(TerminalProvider); ok {
		s = s.With(CapTerminal)
	}
	if _, ok := rt.(HealthChecker); ok {
		s = s.With(CapHealth)
	}
	if _, ok := rt.(Inspector); ok {
		s = s.With(CapInspect)
	}
	if _, ok := rt.(Updater); ok {
		s = s.With(CapUpdate)
	}
	if _, ok := rt.(ConsoleProvider); ok {
		s = s.With(CapConsole)
	}
	return s
}

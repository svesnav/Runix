// Package protocol defines the wire format between the control plane and
// agents: one WebSocket connection per agent carrying hello/heartbeat,
// request/response RPC (correlated by ID) and multiplexed byte streams
// (logs, terminals). Both sides import this package and nothing else from
// each other.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/runix/runix/internal/domain/runtime"
)

const Version = 1

type MessageType string

const (
	TypeHello     MessageType = "hello"     // agent → server, first frame
	TypeWelcome   MessageType = "welcome"   // server → agent, accepts the agent
	TypeHeartbeat MessageType = "heartbeat" // agent → server, periodic
	TypeRequest   MessageType = "req"       // server → agent RPC call
	TypeResponse  MessageType = "resp"      // agent → server RPC result
	TypeStream    MessageType = "stream"    // either direction, stream frame
	TypeEvent     MessageType = "event"     // agent → server, unsolicited
)

type StreamOp string

const (
	StreamOpen  StreamOp = "open"  // server → agent, starts a stream (Method set)
	StreamData  StreamOp = "data"  // payload carries Data bytes
	StreamCtrl  StreamOp = "ctrl"  // control message (terminal resize, ...)
	StreamClose StreamOp = "close" // either side ends the stream
)

// Envelope frames every message on the wire.
type Envelope struct {
	V       int             `json:"v"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Op      StreamOp        `json:"op,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Data    []byte          `json:"data,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

const (
	CodeNotFound     = "not_found"
	CodeNotSupported = "not_supported"
	CodeUnavailable  = "unavailable"
	CodeInvalid      = "invalid"
	CodeInternal     = "internal"
	CodeTimeout      = "timeout"
)

// RPC methods implemented by agents.
const (
	MethodPing           = "ping"
	MethodRuntimeList    = "runtime.list"
	MethodRuntimeGet     = "runtime.get"
	MethodRuntimeAction  = "runtime.action"
	MethodRuntimeCreate  = "runtime.create"
	MethodRuntimeUpdate  = "runtime.update"
	MethodRuntimeInspect = "runtime.inspect"
	MethodRuntimeRemove  = "runtime.remove"
	MethodRuntimeExec    = "runtime.exec"
	MethodRuntimeLogs    = "runtime.logs"    // stream
	MethodRuntimeConsole = "runtime.console" // stream, bidirectional (stdin)
	MethodTerminalOpen   = "terminal.open"   // stream, bidirectional
	MethodFSList         = "fs.list"
	MethodFSStat         = "fs.stat"
	MethodFSRead         = "fs.read"
	MethodFSWrite        = "fs.write"
	MethodFSMkdir        = "fs.mkdir"
	MethodFSRename       = "fs.rename"
	MethodFSDelete       = "fs.delete"
	MethodFSCreate       = "fs.create"
	MethodFSCopy         = "fs.copy"
	MethodFSChmod        = "fs.chmod"
	MethodFSArchive      = "fs.archive"
	MethodFSExtract      = "fs.extract"
	MethodFSDownload     = "fs.download" // stream, agent → server
	MethodFSUpload       = "fs.upload"   // stream, server → agent

	// Docker object management. Images, volumes and networks are
	// Docker-specific resources rather than runtimes, so they get their own
	// methods instead of being forced into the runtime abstraction.
	MethodDockerResourceList   = "docker.resource.list"
	MethodDockerResourceCreate = "docker.resource.create"
	MethodDockerResourceRemove = "docker.resource.remove"
	MethodDockerResourcePrune  = "docker.resource.prune"
	MethodDockerDiskUsage      = "docker.diskusage"

	// Agent lifecycle.
	MethodAgentUpdate  = "agent.update"
	MethodAgentPlugins = "agent.plugins"
)

// AgentUpdateParams instructs the agent to replace its own binary. The
// checksum is mandatory: the agent runs as root, so an unverified download
// would be a remote-code-execution channel.
type AgentUpdateParams struct {
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Version string `json:"version,omitempty"`
	Restart bool   `json:"restart"`
}

type AgentUpdateResult struct {
	PreviousVersion string `json:"previousVersion"`
	InstalledPath   string `json:"installedPath"`
	Restarting      bool   `json:"restarting"`
}

// PluginInfo describes an external runtime provider discovered by the agent.
type PluginInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	RuntimeType string `json:"runtimeType"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
}

// Docker resource kinds.
const (
	DockerImages   = "images"
	DockerVolumes  = "volumes"
	DockerNetworks = "networks"
)

type DockerResourceParams struct {
	Kind string `json:"kind"`
	// ID addresses an existing object (image id/tag, volume or network name).
	ID    string `json:"id,omitempty"`
	Force bool   `json:"force,omitempty"`
	// Create fields.
	Name     string            `json:"name,omitempty"`
	Driver   string            `json:"driver,omitempty"`
	Internal bool              `json:"internal,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	// Image pull reference for kind=images on create.
	Image string `json:"image,omitempty"`
}

// Handshake ------------------------------------------------------------------

type HostInfo struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	OSVersion     string `json:"osVersion"`
	KernelVersion string `json:"kernelVersion"`
	Architecture  string `json:"architecture"`
	AgentVersion  string `json:"agentVersion"`
	CPUCores      int    `json:"cpuCores"`
	MemoryTotal   uint64 `json:"memoryTotal"`
	SwapTotal     uint64 `json:"swapTotal"`
	DiskTotal     uint64 `json:"diskTotal"`
}

type ProviderInfo struct {
	Type         string   `json:"type"`
	Available    bool     `json:"available"`
	Version      string   `json:"version,omitempty"`
	Message      string   `json:"message,omitempty"`
	Capabilities []string `json:"capabilities"`
}

type Hello struct {
	Info      HostInfo       `json:"info"`
	Providers []ProviderInfo `json:"providers"`
}

type Welcome struct {
	ServerID         string `json:"serverId"`
	HeartbeatSeconds int    `json:"heartbeatSeconds"`
}

// Heartbeat ------------------------------------------------------------------

type HostMetrics struct {
	CPUPercent    float64  `json:"cpuPercent"`
	Load1         float64  `json:"load1"`
	Load5         float64  `json:"load5"`
	Load15        float64  `json:"load15"`
	MemoryUsed    uint64   `json:"memoryUsed"`
	MemoryTotal   uint64   `json:"memoryTotal"`
	SwapUsed      uint64   `json:"swapUsed"`
	SwapTotal     uint64   `json:"swapTotal"`
	DiskUsed      uint64   `json:"diskUsed"`
	DiskTotal     uint64   `json:"diskTotal"`
	NetRxBytes    uint64   `json:"netRxBytes"`
	NetTxBytes    uint64   `json:"netTxBytes"`
	Temperature   *float64 `json:"temperature,omitempty"`
	UptimeSeconds uint64   `json:"uptimeSeconds"`
}

type RuntimeCounts struct {
	Type   string         `json:"type"`
	States map[string]int `json:"states"`
}

type Heartbeat struct {
	Metrics   HostMetrics     `json:"metrics"`
	Runtimes  []RuntimeCounts `json:"runtimes"`
	Providers []ProviderInfo  `json:"providers"`
}

// Runtime RPC ----------------------------------------------------------------

type RuntimeInfo struct {
	Descriptor   runtime.Descriptor `json:"descriptor"`
	Capabilities []string           `json:"capabilities"`
}

type RuntimeListParams struct {
	Type string `json:"type,omitempty"` // empty = every provider
}

type RuntimeListResult struct {
	Runtimes []RuntimeInfo `json:"runtimes"`
}

type RuntimeGetParams struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Runtime lifecycle actions. Providers may accept extra provider-specific
// actions (systemd: enable/disable/mask/unmask).
const (
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
	ActionPause   = "pause"
	ActionResume  = "resume"
	ActionKill    = "kill"
	ActionReload  = "reload"
)

type RuntimeActionParams struct {
	Type   string              `json:"type"`
	ID     string              `json:"id"`
	Action string              `json:"action"`
	Stop   runtime.StopOptions `json:"stop,omitempty"`
	Signal string              `json:"signal,omitempty"`
}

type RuntimeCreateParams struct {
	Spec runtime.Spec `json:"spec"`
}

type RuntimeUpdateParams struct {
	Type string       `json:"type"`
	ID   string       `json:"id"`
	Spec runtime.Spec `json:"spec"`
}

type RuntimeInspectParams struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type RuntimeRemoveParams struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Force bool   `json:"force,omitempty"`
	Purge bool   `json:"purge,omitempty"`
}

type RuntimeExecParams struct {
	Type           string   `json:"type"`
	ID             string   `json:"id"`
	Cmd            []string `json:"cmd"`
	Env            []string `json:"env,omitempty"`
	WorkingDir     string   `json:"workingDir,omitempty"`
	User           string   `json:"user,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

type RuntimeExecResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    []byte `json:"stdout,omitempty"`
	Stderr    []byte `json:"stderr,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type RuntimeLogsParams struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Follow     bool   `json:"follow,omitempty"`
	Tail       int    `json:"tail,omitempty"`
	Timestamps bool   `json:"timestamps,omitempty"`
}

// RuntimeConsoleParams opens the main process's standard streams: output
// arrives as data frames, input is written back on the same stream. This is
// what a game server or other console-driven program needs, as opposed to
// terminal.open which starts a separate shell beside the process.
type RuntimeConsoleParams struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	// Tail replays this many recent output lines before live output, so the
	// console is not blank on connect.
	Tail int `json:"tail,omitempty"`
}

// LogLine is the payload of each runtime.logs stream data frame.
type LogLine struct {
	Timestamp time.Time `json:"timestamp,omitempty"`
	Source    string    `json:"source,omitempty"`
	Line      string    `json:"line"`
}

// Terminal -------------------------------------------------------------------

const (
	TerminalTargetHost    = "host"
	TerminalTargetRuntime = "runtime"
)

type TerminalParams struct {
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
}

// TerminalCtrl rides in StreamCtrl frames.
type TerminalCtrl struct {
	Resize *TerminalResize `json:"resize,omitempty"`
}

type TerminalResize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Filesystem -----------------------------------------------------------------

type FSListParams struct {
	Path       string `json:"path"`
	ShowHidden bool   `json:"showHidden,omitempty"`
}

type FSEntry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Mode      string    `json:"mode"`
	ModTime   time.Time `json:"modTime"`
	IsDir     bool      `json:"isDir"`
	IsSymlink bool      `json:"isSymlink"`
}

type FSListResult struct {
	Path    string    `json:"path"`
	Entries []FSEntry `json:"entries"`
}

type FSStatParams struct {
	Path string `json:"path"`
}

type FSReadParams struct {
	Path     string `json:"path"`
	MaxBytes int64  `json:"maxBytes,omitempty"`
}

type FSReadResult struct {
	Content   []byte `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
}

type FSWriteParams struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
	Mode    uint32 `json:"mode,omitempty"`
	Append  bool   `json:"append,omitempty"`
}

type FSMkdirParams struct {
	Path string `json:"path"`
}

type FSRenameParams struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type FSDeleteParams struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

type FSCreateParams struct {
	Path string `json:"path"`
}

type FSCopyParams struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type FSChmodParams struct {
	Path string `json:"path"`
	// Mode is the octal permission string ("644", "0755").
	Mode      string `json:"mode"`
	Recursive bool   `json:"recursive,omitempty"`
}

// Archive formats supported by fs.archive / fs.extract.
const (
	ArchiveTarGz = "tar.gz"
	ArchiveZip   = "zip"
)

type FSArchiveParams struct {
	// Paths are the absolute sources to include; each is added under its
	// base name so archives stay relocatable.
	Paths  []string `json:"paths"`
	Target string   `json:"target"`
	Format string   `json:"format"`
}

type FSExtractParams struct {
	Path string `json:"path"`
	// Dest defaults to the archive's directory when empty.
	Dest string `json:"dest,omitempty"`
}

type FSDownloadParams struct {
	Path string `json:"path"`
	// Archive requests an on-the-fly tar.gz of a directory or of several
	// paths, so multi-select downloads arrive as one file.
	Paths   []string `json:"paths,omitempty"`
	Archive bool     `json:"archive,omitempty"`
}

// FSDownloadMeta is the first frame of a download stream, sent as JSON in a
// StreamCtrl frame before the binary data frames.
type FSDownloadMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type FSUploadParams struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
}

// FSUploadCtrl signals the end of an upload; without EOF the agent treats a
// closed stream as an abort and discards the partial file.
type FSUploadCtrl struct {
	EOF bool `json:"eof"`
}

// Helpers --------------------------------------------------------------------

func Marshal(t MessageType, id string, payload any) (Envelope, error) {
	env := Envelope{V: Version, Type: t, ID: id}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, fmt.Errorf("protocol: marshal payload: %w", err)
		}
		env.Payload = raw
	}
	return env, nil
}

func Decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("protocol: decode payload: %w", err)
	}
	return v, nil
}

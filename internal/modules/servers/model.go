package servers

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("servers: not found")
	ErrConflict = errors.New("servers: name already taken")
	ErrInvalid  = errors.New("servers: invalid input")
)

type ConnectionStatus string

const (
	StatusNeverConnected ConnectionStatus = "never_connected"
	StatusOnline         ConnectionStatus = "online"
	StatusOffline        ConnectionStatus = "offline"
)

type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Address is the operator-recorded IP or domain of the host. Purely
	// informational: agents dial out, the control plane never connects in.
	Address          string            `json:"address"`
	Description      string            `json:"description"`
	Hostname         string            `json:"hostname"`
	OS               string            `json:"os"`
	OSVersion        string            `json:"osVersion"`
	KernelVersion    string            `json:"kernelVersion"`
	Architecture     string            `json:"architecture"`
	AgentVersion     string            `json:"agentVersion"`
	Location         string            `json:"location"`
	Tags             []string          `json:"tags"`
	Labels           map[string]string `json:"labels"`
	AgentTokenHash   []byte            `json:"-"`
	CPUCores         int               `json:"cpuCores"`
	MemoryBytes      uint64            `json:"memoryBytes"`
	SwapBytes        uint64            `json:"swapBytes"`
	DiskBytes        uint64            `json:"diskBytes"`
	DockerAvailable  bool              `json:"dockerAvailable"`
	SystemdAvailable bool              `json:"systemdAvailable"`
	RuntimeTypes     []string          `json:"runtimeTypes"`
	ConnectionStatus ConnectionStatus  `json:"connectionStatus"`
	LastHeartbeatAt  *time.Time        `json:"lastHeartbeatAt,omitempty"`
	LastSeenAt       *time.Time        `json:"lastSeenAt,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

// MetricsPoint is one persisted heartbeat sample.
type MetricsPoint struct {
	ServerID    string    `json:"serverId"`
	CollectedAt time.Time `json:"collectedAt"`
	CPUPercent  float64   `json:"cpuPercent"`
	Load1       float64   `json:"load1"`
	Load5       float64   `json:"load5"`
	Load15      float64   `json:"load15"`
	MemoryUsed  uint64    `json:"memoryUsed"`
	MemoryTotal uint64    `json:"memoryTotal"`
	SwapUsed    uint64    `json:"swapUsed"`
	SwapTotal   uint64    `json:"swapTotal"`
	DiskUsed    uint64    `json:"diskUsed"`
	DiskTotal   uint64    `json:"diskTotal"`
	NetRxBytes  uint64    `json:"netRxBytes"`
	NetTxBytes  uint64    `json:"netTxBytes"`
	Temperature *float64  `json:"temperature,omitempty"`
	UptimeSecs  uint64    `json:"uptimeSecs"`
}

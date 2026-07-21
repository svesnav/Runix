package scheduler

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("scheduler: task not found")
	ErrConflict = errors.New("scheduler: a task with that name already exists on this server")
	ErrInvalid  = errors.New("scheduler: invalid task")
)

// Task kinds. Both act on a runtime through the agent, which keeps the
// scheduler free of any per-technology knowledge.
const (
	KindRuntimeAction = "runtime_action"
	KindRuntimeExec   = "runtime_exec"
)

const (
	StatusSuccess = "success"
	StatusFailure = "failure"
)

// Payload carries the kind-specific parameters.
type Payload struct {
	RuntimeType string   `json:"runtimeType"`
	RuntimeID   string   `json:"runtimeId"`
	Action      string   `json:"action,omitempty"` // runtime_action
	Cmd         []string `json:"cmd,omitempty"`    // runtime_exec
}

type Task struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ServerID    string     `json:"serverId"`
	Kind        string     `json:"kind"`
	Payload     Payload    `json:"payload"`
	Cron        string     `json:"cron"`
	Enabled     bool       `json:"enabled"`
	NextRunAt   *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
	LastStatus  string     `json:"lastStatus,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type Run struct {
	ID         int64     `json:"id"`
	TaskID     string    `json:"taskId"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int       `json:"durationMs"`
	Status     string    `json:"status"`
	Detail     string    `json:"detail,omitempty"`
}

func (p Payload) marshal() ([]byte, error) { return json.Marshal(p) }

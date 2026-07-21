package audit

import (
	"encoding/json"
	"time"
)

const (
	ResultSuccess = "success"
	ResultFailure = "failure"
)

// Entry is one immutable audit record.
type Entry struct {
	ID         int64           `json:"id"`
	Time       time.Time       `json:"time"`
	ActorID    string          `json:"actorId,omitempty"`
	ActorName  string          `json:"actorName,omitempty"`
	IP         string          `json:"ip,omitempty"`
	UserAgent  string          `json:"userAgent,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType,omitempty"`
	TargetID   string          `json:"targetId,omitempty"`
	OldValue   json.RawMessage `json:"oldValue,omitempty"`
	NewValue   json.RawMessage `json:"newValue,omitempty"`
	Result     string          `json:"result"`
	Error      string          `json:"error,omitempty"`
}

// Filter narrows audit listings.
type Filter struct {
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	From       time.Time
	To         time.Time
}

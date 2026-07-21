package settings

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("settings: not found")
	ErrUnknownKey = errors.New("settings: unknown key")
	ErrInvalid    = errors.New("settings: invalid value")
)

// Setting is one platform-wide configuration value.
type Setting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updatedAt"`
	UpdatedBy string          `json:"updatedBy,omitempty"`
}

// Known keys and their validators. Settings are code-declared so a typo'd
// key is an error, not silent dead configuration.
var registry = map[string]func(json.RawMessage) error{
	"platform.name":            validateString(1, 64),
	"metrics.retention_days":   validateIntRange(1, 365),
	"sessions.cleanup_enabled": validateBool,
	"agents.offline_after_sec": validateIntRange(30, 3600),
}

func KnownKeys() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

func validateString(minLen, maxLen int) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ErrInvalid
		}
		if len(s) < minLen || len(s) > maxLen {
			return ErrInvalid
		}
		return nil
	}
}

func validateIntRange(minV, maxV int) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return ErrInvalid
		}
		if n < minV || n > maxV {
			return ErrInvalid
		}
		return nil
	}
}

func validateBool(raw json.RawMessage) error {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return ErrInvalid
	}
	return nil
}

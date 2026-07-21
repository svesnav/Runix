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

// Kind tells a client which control to render, so the UI never has to
// pattern-match on key names or make the operator hand-write JSON.
type Kind string

const (
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindBool   Kind = "bool"
)

// Descriptor is everything a client needs to present one setting. The
// dotted Key stays the stored identifier — renaming it would be a data
// migration for no functional gain — while Label carries the wording.
// Clients may translate by Key and fall back to Label.
type Descriptor struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Group       string          `json:"group"`
	Kind        Kind            `json:"kind"`
	Unit        string          `json:"unit,omitempty"`
	Min         *int            `json:"min,omitempty"`
	Max         *int            `json:"max,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`

	validate func(json.RawMessage) error
}

func intp(v int) *int { return &v }

// Settings are code-declared so a typo'd key is an error, not silent dead
// configuration. Order here is the order clients display.
var descriptors = []Descriptor{
	{
		Key: "platform.name", Group: "general", Kind: KindString,
		Label:       "Platform name",
		Description: "Shown in the browser title and on the sign-in page.",
		Default:     json.RawMessage(`"Runix"`),
		validate:    validateString(1, 64),
	},
	{
		Key: "metrics.retention_days", Group: "general", Kind: KindInt,
		Label:       "Keep metrics history for",
		Description: "Older samples are deleted. Longer retention costs database space.",
		Unit:        "days", Min: intp(1), Max: intp(365),
		Default:  json.RawMessage(`30`),
		validate: validateIntRange(1, 365),
	},
	{
		Key: "agents.offline_after_sec", Group: "agents", Kind: KindInt,
		Label:       "Mark an agent offline after",
		Description: "How long a host may go without a heartbeat before it is shown as offline.",
		Unit:        "seconds", Min: intp(30), Max: intp(3600),
		Default:  json.RawMessage(`120`),
		validate: validateIntRange(30, 3600),
	},
	{
		Key: "sessions.cleanup_enabled", Group: "security", Kind: KindBool,
		Label:       "Clean up expired sessions",
		Description: "Periodically delete refresh tokens that have already expired.",
		Default:     json.RawMessage(`true`),
		validate:    validateBool,
	},
}

var registry = func() map[string]func(json.RawMessage) error {
	m := make(map[string]func(json.RawMessage) error, len(descriptors))
	for _, d := range descriptors {
		m[d.Key] = d.validate
	}
	return m
}()

// Descriptors describes every known setting, in display order.
func Descriptors() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

func KnownKeys() []string {
	out := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, d.Key)
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

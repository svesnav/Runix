package runtime

import "errors"

// Sentinel errors shared by every provider implementation. Providers wrap
// these with context so callers can branch on errors.Is while logs stay
// descriptive.
var (
	ErrNotFound          = errors.New("runtime: not found")
	ErrAlreadyExists     = errors.New("runtime: already exists")
	ErrNotSupported      = errors.New("runtime: operation not supported")
	ErrUnavailable       = errors.New("runtime: provider unavailable")
	ErrInvalidSpec       = errors.New("runtime: invalid spec")
	ErrInvalidTransition = errors.New("runtime: invalid state transition")
)

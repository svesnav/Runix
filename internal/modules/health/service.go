package health

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Check probes one dependency (database, redis, ...). A nil error means
// ready.
type Check func(ctx context.Context) error

// Service tracks process uptime and the registered readiness checks.
type Service struct {
	startedAt time.Time

	mu     sync.RWMutex
	checks map[string]Check
}

func NewService() *Service {
	return &Service{
		startedAt: time.Now(),
		checks:    make(map[string]Check),
	}
}

// Register adds a named readiness check. Infrastructure components register
// themselves during startup wiring.
func (s *Service) Register(name string, c Check) error {
	if name == "" || c == nil {
		return fmt.Errorf("health: check name and func must be set")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.checks[name]; dup {
		return fmt.Errorf("health: check %q already registered", name)
	}
	s.checks[name] = c
	return nil
}

func (s *Service) Uptime() time.Duration {
	return time.Since(s.startedAt)
}

// Run executes every registered check and returns its result by name.
func (s *Service) Run(ctx context.Context) map[string]error {
	s.mu.RLock()
	checks := make(map[string]Check, len(s.checks))
	for name, c := range s.checks {
		checks[name] = c
	}
	s.mu.RUnlock()

	results := make(map[string]error, len(checks))
	for name, c := range checks {
		results[name] = c(ctx)
	}
	return results
}

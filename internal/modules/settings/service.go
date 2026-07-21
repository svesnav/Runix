package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/runix/runix/internal/platform/authn"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]Setting, error) {
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, key string) (Setting, error) {
	if _, known := registry[key]; !known {
		return Setting{}, fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	return s.repo.Get(ctx, key)
}

func (s *Service) Set(ctx context.Context, key string, value json.RawMessage) (Setting, error) {
	validate, known := registry[key]
	if !known {
		return Setting{}, fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	if err := validate(value); err != nil {
		return Setting{}, fmt.Errorf("%w for key %q", err, key)
	}
	setting := Setting{Key: key, Value: value}
	if p, ok := authn.FromContext(ctx); ok {
		setting.UpdatedBy = p.UserID
	}
	return s.repo.Upsert(ctx, setting)
}

// Int reads an integer setting with a fallback default; background workers
// use this for tunables like retention.
func (s *Service) Int(ctx context.Context, key string, def int) int {
	setting, err := s.repo.Get(ctx, key)
	if err != nil {
		return def
	}
	var n int
	if json.Unmarshal(setting.Value, &n) != nil {
		return def
	}
	return n
}

package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
)

// Record describes one auditable action from the caller's point of view;
// actor and transport facts are filled in from context.
type Record struct {
	Action     string
	TargetType string
	TargetID   string
	Old        any
	New        any
	Err        error
}

type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Write persists an audit entry. Failures are logged, never propagated: an
// audit outage must not take user-facing operations down with it.
func (s *Service) Write(ctx context.Context, rec Record) {
	e := Entry{
		Time:       time.Now(),
		Action:     rec.Action,
		TargetType: rec.TargetType,
		TargetID:   rec.TargetID,
		Result:     ResultSuccess,
	}
	if p, ok := authn.FromContext(ctx); ok {
		e.ActorID = p.UserID
		e.ActorName = p.Username
	}
	meta := authn.RequestMetaFromContext(ctx)
	e.IP, e.UserAgent, e.RequestID = meta.IP, meta.UserAgent, meta.RequestID

	if rec.Err != nil {
		e.Result = ResultFailure
		e.Error = rec.Err.Error()
	}
	e.OldValue = marshal(rec.Old)
	e.NewValue = marshal(rec.New)

	// Detach from the request context so cancellation doesn't lose the entry.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.repo.Insert(writeCtx, e); err != nil {
		s.log.Error("audit write failed", "action", rec.Action, "err", err)
	}
}

func (s *Service) List(ctx context.Context, f Filter, page httpx.Page) ([]Entry, int64, error) {
	return s.repo.List(ctx, f, page)
}

func marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"_marshal_error":true}`)
	}
	return raw
}

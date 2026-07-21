package servers

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/runix/runix/internal/platform/bus"
	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

const agentTokenPrefix = "rnx_agt_"

type Service struct {
	repo Repository
	bus  *bus.Bus
	log  *slog.Logger
}

func NewService(repo Repository, eventBus *bus.Bus, log *slog.Logger) *Service {
	return &Service{repo: repo, bus: eventBus, log: log}
}

type CreateInput struct {
	Name        string
	Description string
	Address     string
	Location    string
	Tags        []string
	Labels      map[string]string
	// AgentToken lets the operator supply a pre-shared token (e.g. baked
	// into an image or config-management secret). Empty means generate one.
	AgentToken string
}

const minSuppliedTokenLen = 24

// Created carries the one-time plaintext agent token.
type Created struct {
	Server     Server `json:"server"`
	AgentToken string `json:"agentToken"`
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Created, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 {
		return Created{}, fmt.Errorf("%w: name must be 1-128 characters", ErrInvalid)
	}
	plain, err := resolveToken(in.AgentToken)
	if err != nil {
		return Created{}, err
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	created, err := s.repo.Create(ctx, Server{
		Name: name, Description: in.Description, Address: in.Address, Location: in.Location,
		Tags: in.Tags, Labels: in.Labels,
		AgentTokenHash: crypto.HashToken(plain),
	})
	if err != nil {
		return Created{}, err
	}
	return Created{Server: created, AgentToken: plain}, nil
}

// resolveToken validates an operator-supplied token or mints a new one.
func resolveToken(supplied string) (string, error) {
	supplied = strings.TrimSpace(supplied)
	if supplied != "" {
		if len(supplied) < minSuppliedTokenLen {
			return "", fmt.Errorf("%w: agent token must be at least %d characters",
				ErrInvalid, minSuppliedTokenLen)
		}
		if strings.ContainsAny(supplied, " \t\r\n") {
			return "", fmt.Errorf("%w: agent token must not contain whitespace", ErrInvalid)
		}
		return supplied, nil
	}
	token, err := crypto.RandomToken(32)
	if err != nil {
		return "", fmt.Errorf("servers: generate token: %w", err)
	}
	return agentTokenPrefix + token, nil
}

// RotateToken invalidates the agent's credential. A supplied token replaces
// the generated one, so operators can pin a known secret.
func (s *Service) RotateToken(ctx context.Context, id, supplied string) (string, error) {
	plain, err := resolveToken(supplied)
	if err != nil {
		return "", err
	}
	if err := s.repo.UpdateTokenHash(ctx, id, crypto.HashToken(plain)); err != nil {
		return "", err
	}
	return plain, nil
}

func (s *Service) Get(ctx context.Context, id string) (Server, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, page httpx.Page) ([]Server, int64, error) {
	return s.repo.List(ctx, page)
}

func (s *Service) ListAll(ctx context.Context) ([]Server, error) {
	return s.repo.ListAll(ctx)
}

type UpdateInput struct {
	Name        string
	Description string
	Address     string
	Location    string
	Tags        []string
	Labels      map[string]string
}

func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (Server, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 {
		return Server{}, fmt.Errorf("%w: name must be 1-128 characters", ErrInvalid)
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	return s.repo.Update(ctx, Server{
		ID: id, Name: name, Description: in.Description, Address: in.Address, Location: in.Location,
		Tags: in.Tags, Labels: in.Labels,
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// Authenticate resolves an agent bearer token to its server; used by the
// hub when an agent connects.
// Authenticate resolves an agent bearer token to its server. Operator-
// supplied tokens need not carry the generated prefix, so identification is
// purely by hash against the servers table.
func (s *Service) Authenticate(ctx context.Context, token string) (Server, error) {
	token = strings.TrimSpace(token)
	if len(token) < minSuppliedTokenLen {
		return Server{}, ErrNotFound
	}
	return s.repo.GetByTokenHash(ctx, crypto.HashToken(token))
}

// ApplyHello records agent-reported host facts on connect.
func (s *Service) ApplyHello(ctx context.Context, serverID string, hello protocol.Hello) error {
	srv := Server{
		ID:            serverID,
		Hostname:      hello.Info.Hostname,
		OS:            hello.Info.OS,
		OSVersion:     hello.Info.OSVersion,
		KernelVersion: hello.Info.KernelVersion,
		Architecture:  hello.Info.Architecture,
		AgentVersion:  hello.Info.AgentVersion,
		CPUCores:      hello.Info.CPUCores,
		MemoryBytes:   hello.Info.MemoryTotal,
		SwapBytes:     hello.Info.SwapTotal,
		DiskBytes:     hello.Info.DiskTotal,
		RuntimeTypes:  []string{},
	}
	for _, p := range hello.Providers {
		if p.Available {
			srv.RuntimeTypes = append(srv.RuntimeTypes, p.Type)
		}
		switch p.Type {
		case "docker":
			srv.DockerAvailable = p.Available
		case "systemd":
			srv.SystemdAvailable = p.Available
		}
	}
	slices.Sort(srv.RuntimeTypes)
	return s.repo.ApplyAgentFacts(ctx, srv)
}

// Reconcilepresence flips stale online rows to offline after a control
// plane restart.
func (s *Service) ReconcilePresence(ctx context.Context) error {
	return s.repo.MarkAllOffline(ctx)
}

func (s *Service) MarkOnline(ctx context.Context, serverID string) {
	if err := s.repo.SetConnectionStatus(ctx, serverID, StatusOnline); err != nil {
		s.log.Error("mark online failed", "server", serverID, "err", err)
	}
	s.bus.Publish(bus.Event{Topic: "agent.online", ServerID: serverID})
}

func (s *Service) MarkOffline(ctx context.Context, serverID string) {
	if err := s.repo.SetConnectionStatus(ctx, serverID, StatusOffline); err != nil {
		s.log.Error("mark offline failed", "server", serverID, "err", err)
	}
	s.bus.Publish(bus.Event{Topic: "agent.offline", ServerID: serverID})
}

// RecordHeartbeat persists the sample and fans it out to live subscribers.
func (s *Service) RecordHeartbeat(ctx context.Context, serverID string, hb protocol.Heartbeat) error {
	point := MetricsPoint{
		ServerID:    serverID,
		CollectedAt: time.Now().UTC().Truncate(time.Second),
		CPUPercent:  hb.Metrics.CPUPercent,
		Load1:       hb.Metrics.Load1,
		Load5:       hb.Metrics.Load5,
		Load15:      hb.Metrics.Load15,
		MemoryUsed:  hb.Metrics.MemoryUsed,
		MemoryTotal: hb.Metrics.MemoryTotal,
		SwapUsed:    hb.Metrics.SwapUsed,
		SwapTotal:   hb.Metrics.SwapTotal,
		DiskUsed:    hb.Metrics.DiskUsed,
		DiskTotal:   hb.Metrics.DiskTotal,
		NetRxBytes:  hb.Metrics.NetRxBytes,
		NetTxBytes:  hb.Metrics.NetTxBytes,
		Temperature: hb.Metrics.Temperature,
		UptimeSecs:  hb.Metrics.UptimeSeconds,
	}
	if err := s.repo.RecordHeartbeat(ctx, serverID, point); err != nil {
		return err
	}
	s.bus.Publish(bus.Event{Topic: "agent.heartbeat", ServerID: serverID, Payload: hb})
	return nil
}

func (s *Service) Metrics(ctx context.Context, serverID string, from, to time.Time, limit int) ([]MetricsPoint, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	return s.repo.Metrics(ctx, serverID, from, to, limit)
}

func (s *Service) PruneMetrics(ctx context.Context, retention time.Duration) {
	n, err := s.repo.PruneMetrics(ctx, time.Now().Add(-retention))
	if err != nil {
		s.log.Error("metrics pruning failed", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("pruned metrics", "rows", n)
	}
}

func (s *Service) CountByStatus(ctx context.Context) (map[ConnectionStatus]int, error) {
	return s.repo.CountByStatus(ctx)
}

// Groups ---------------------------------------------------------------------

func (s *Service) CreateGroup(ctx context.Context, name, description string) (Group, error) {
	if strings.TrimSpace(name) == "" {
		return Group{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	return s.repo.CreateGroup(ctx, Group{Name: name, Description: description})
}

func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.repo.ListGroups(ctx)
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	return s.repo.DeleteGroup(ctx, id)
}

func (s *Service) AddGroupMember(ctx context.Context, groupID, serverID string) error {
	return s.repo.AddGroupMember(ctx, groupID, serverID)
}

func (s *Service) RemoveGroupMember(ctx context.Context, groupID, serverID string) error {
	return s.repo.RemoveGroupMember(ctx, groupID, serverID)
}

// GroupIDsOfServer implements rbac.ServerGroupResolver.
func (s *Service) GroupIDsOfServer(ctx context.Context, serverID string) ([]string, error) {
	return s.repo.GroupIDsOfServer(ctx, serverID)
}

func (s *Service) ServerIDsOfGroup(ctx context.Context, groupID string) ([]string, error) {
	return s.repo.ServerIDsOfGroup(ctx, groupID)
}

// Package backup exports and restores the platform's configuration as a
// portable JSON document: servers, groups, roles, grants, scheduled tasks
// and settings.
//
// Deliberately excluded: user passwords, agent tokens, TOTP secrets and
// sessions. A configuration backup is not a secrets vault — restoring one
// re-creates structure, and credentials are re-issued afterwards. Metrics
// and audit history are also excluded; they are operational data, not
// configuration.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/runix/runix/internal/modules/rbac"
	"github.com/runix/runix/internal/modules/scheduler"
	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/modules/settings"
	"github.com/runix/runix/internal/platform/version"
)

const FormatVersion = 1

type Document struct {
	Format     int                `json:"format"`
	CreatedAt  time.Time          `json:"createdAt"`
	Runix      string             `json:"runixVersion"`
	Servers    []ServerEntry      `json:"servers"`
	ServerGrps []servers.Group    `json:"serverGroups"`
	Roles      []rbac.Role        `json:"roles"`
	UserGroups []rbac.Group       `json:"userGroups"`
	Grants     []rbac.Grant       `json:"grants"`
	Tasks      []scheduler.Task   `json:"scheduledTasks"`
	Settings   []settings.Setting `json:"settings"`
}

// ServerEntry is a server without its credential or live state.
type ServerEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Address     string            `json:"address"`
	Location    string            `json:"location"`
	Tags        []string          `json:"tags"`
	Labels      map[string]string `json:"labels"`
}

// Report summarizes what an import did.
type Report struct {
	Created map[string]int `json:"created"`
	Skipped map[string]int `json:"skipped"`
	Errors  []string       `json:"errors,omitempty"`
}

type Service struct {
	servers   *servers.Service
	rbac      *rbac.Service
	scheduler *scheduler.Service
	settings  *settings.Service
	log       *slog.Logger
}

func NewService(srv *servers.Service, rb *rbac.Service, sched *scheduler.Service,
	set *settings.Service, log *slog.Logger) *Service {
	return &Service{servers: srv, rbac: rb, scheduler: sched, settings: set, log: log}
}

func (s *Service) Export(ctx context.Context) (Document, error) {
	doc := Document{
		Format:    FormatVersion,
		CreatedAt: time.Now().UTC(),
		Runix:     version.Get().Version,
	}

	serverList, err := s.servers.ListAll(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("backup: servers: %w", err)
	}
	for _, srv := range serverList {
		doc.Servers = append(doc.Servers, ServerEntry{
			Name: srv.Name, Description: srv.Description, Address: srv.Address,
			Location: srv.Location, Tags: srv.Tags, Labels: srv.Labels,
		})
	}
	if doc.ServerGrps, err = s.servers.ListGroups(ctx); err != nil {
		return Document{}, fmt.Errorf("backup: server groups: %w", err)
	}
	if doc.Roles, err = s.rbac.ListRoles(ctx); err != nil {
		return Document{}, fmt.Errorf("backup: roles: %w", err)
	}
	if doc.UserGroups, err = s.rbac.ListGroups(ctx); err != nil {
		return Document{}, fmt.Errorf("backup: user groups: %w", err)
	}
	if doc.Grants, err = s.rbac.ListGrants(ctx); err != nil {
		return Document{}, fmt.Errorf("backup: grants: %w", err)
	}
	if doc.Tasks, err = s.scheduler.List(ctx, ""); err != nil {
		return Document{}, fmt.Errorf("backup: scheduled tasks: %w", err)
	}
	if doc.Settings, err = s.settings.List(ctx); err != nil {
		return Document{}, fmt.Errorf("backup: settings: %w", err)
	}
	return doc, nil
}

// Import re-creates missing objects. It is additive and idempotent:
// anything that already exists is skipped rather than overwritten, so a
// restore can never silently destroy live configuration.
func (s *Service) Import(ctx context.Context, doc Document) (Report, error) {
	if doc.Format != FormatVersion {
		return Report{}, fmt.Errorf("backup: unsupported format version %d (expected %d)",
			doc.Format, FormatVersion)
	}
	report := Report{Created: map[string]int{}, Skipped: map[string]int{}}
	note := func(kind string, err error) {
		if err == nil {
			report.Created[kind]++
			return
		}
		report.Skipped[kind]++
		s.log.Debug("backup import skipped an object", "kind", kind, "err", err)
	}

	existingServers, err := s.servers.ListAll(ctx)
	if err != nil {
		return Report{}, err
	}
	haveServer := map[string]bool{}
	for _, srv := range existingServers {
		haveServer[srv.Name] = true
	}
	for _, entry := range doc.Servers {
		if haveServer[entry.Name] {
			report.Skipped["servers"]++
			continue
		}
		// A restored server gets a fresh agent token; the old one is not in
		// the backup by design.
		_, err := s.servers.Create(ctx, servers.CreateInput{
			Name: entry.Name, Description: entry.Description, Address: entry.Address,
			Location: entry.Location, Tags: entry.Tags, Labels: entry.Labels,
		})
		note("servers", err)
	}

	for _, group := range doc.ServerGrps {
		_, err := s.servers.CreateGroup(ctx, group.Name, group.Description)
		note("serverGroups", err)
	}
	for _, group := range doc.UserGroups {
		_, err := s.rbac.CreateGroup(ctx, group.Name, group.Description)
		note("userGroups", err)
	}
	for _, role := range doc.Roles {
		if role.IsSystem {
			report.Skipped["roles"]++
			continue
		}
		_, err := s.rbac.CreateRole(ctx, role.Key, role.Name, role.Description, role.Permissions)
		note("roles", err)
	}
	for _, setting := range doc.Settings {
		_, err := s.settings.Set(ctx, setting.Key, setting.Value)
		note("settings", err)
	}

	// Grants and scheduled tasks reference ids that differ after a restore
	// onto a fresh database, so they are reported rather than guessed at.
	if len(doc.Grants) > 0 {
		report.Skipped["grants"] = len(doc.Grants)
		report.Errors = append(report.Errors,
			"grants reference user and server ids that change on restore; re-create them from the Grants page")
	}
	if len(doc.Tasks) > 0 {
		report.Skipped["scheduledTasks"] = len(doc.Tasks)
		report.Errors = append(report.Errors,
			"scheduled tasks reference server ids that change on restore; re-create them from the Schedule page")
	}
	return report, nil
}

// Marshal renders the document for download.
func Marshal(doc Document) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

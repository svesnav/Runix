// Package systemd manages systemd services through systemctl/journalctl.
// The CLI is used instead of the D-Bus API to avoid a bus dependency; every
// invocation is argument-vector based (no shell), so unit names cannot
// inject commands.
package systemd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/runix/runix/internal/agent/providers/execx"
	rt "github.com/runix/runix/internal/domain/runtime"
)

var unitNamePattern = regexp.MustCompile(`^[a-zA-Z0-9:._\\@-]+\.(service|timer|socket|mount|target)$`)

type Provider struct{}

func NewProvider() *Provider { return &Provider{} }

func (p *Provider) Type() rt.Type { return rt.TypeSystemd }

func (p *Provider) Capabilities() rt.CapabilitySet {
	return rt.NewCapabilitySet(
		rt.CapStart, rt.CapStop, rt.CapRestart, rt.CapReload, rt.CapKill,
		rt.CapLogs, rt.CapMetrics, rt.CapHealth, rt.CapInspect,
	)
}

func (p *Provider) Availability(ctx context.Context) rt.Availability {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return rt.Availability{Available: false, Message: "systemd is not the active init system"}
	}
	out, err := execx.Run(ctx, "systemctl", "--version")
	if err != nil {
		return rt.Availability{Available: false, Message: err.Error()}
	}
	version := ""
	if fields := strings.Fields(string(out)); len(fields) >= 2 {
		version = fields[1]
	}
	return rt.Availability{Available: true, Version: version}
}

type listedUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

func (p *Provider) List(ctx context.Context) ([]rt.Descriptor, error) {
	out, err := execx.Run(ctx, "systemctl", "list-units", "--type=service",
		"--all", "--no-pager", "--output=json")
	if err != nil {
		return nil, fmt.Errorf("systemd: list units: %w", err)
	}
	var units []listedUnit
	if err := json.Unmarshal(out, &units); err != nil {
		return nil, fmt.Errorf("systemd: parse list-units: %w", err)
	}
	descriptors := make([]rt.Descriptor, 0, len(units))
	for _, u := range units {
		descriptors = append(descriptors, rt.Descriptor{
			ID:   rt.ID(u.Unit),
			Type: rt.TypeSystemd,
			Name: u.Unit,
			Labels: map[string]string{
				"description": u.Description,
				"load":        u.Load,
			},
			Status: rt.Status{
				State:   mapState(u.Active, u.Sub),
				Health:  rt.HealthUnknown,
				Message: u.Active + " (" + u.Sub + ")",
			},
		})
	}
	return descriptors, nil
}

func (p *Provider) Get(ctx context.Context, id rt.ID) (rt.Runtime, error) {
	unit := string(id)
	if !unitNamePattern.MatchString(unit) {
		return nil, fmt.Errorf("%w: unit name %q", rt.ErrInvalidSpec, unit)
	}
	props, err := showUnit(ctx, unit, "LoadState")
	if err != nil {
		return nil, err
	}
	if props["LoadState"] == "not-found" {
		return nil, fmt.Errorf("%w: unit %s", rt.ErrNotFound, unit)
	}
	return &unitRuntime{unit: unit}, nil
}

func (p *Provider) Create(ctx context.Context, spec rt.Spec) (rt.Runtime, error) {
	return nil, fmt.Errorf("%w: creating systemd units (write the unit file via the file manager, then daemon-reload)", rt.ErrNotSupported)
}

func (p *Provider) Remove(ctx context.Context, id rt.ID, opts rt.RemoveOptions) error {
	return fmt.Errorf("%w: removing systemd units", rt.ErrNotSupported)
}

func mapState(active, sub string) rt.State {
	switch active {
	case "active":
		return rt.StateRunning
	case "activating":
		return rt.StateStarting
	case "deactivating":
		return rt.StateStopping
	case "inactive":
		return rt.StateStopped
	case "failed":
		return rt.StateFailed
	case "reloading":
		return rt.StateRunning
	}
	_ = sub
	return rt.StateUnknown
}

func showUnit(ctx context.Context, unit string, properties ...string) (map[string]string, error) {
	args := []string{"show", unit, "--no-pager"}
	if len(properties) > 0 {
		args = append(args, "--property="+strings.Join(properties, ","))
	}
	out, err := execx.Run(ctx, "systemctl", args...)
	if err != nil {
		return nil, fmt.Errorf("systemd: show %s: %w", unit, err)
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	return props, nil
}

func parseSystemdTimestamp(v string) *time.Time {
	if v == "" || v == "n/a" {
		return nil
	}
	// "Mon 2026-07-19 10:11:12 UTC"
	if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", v); err == nil {
		return &t
	}
	return nil
}

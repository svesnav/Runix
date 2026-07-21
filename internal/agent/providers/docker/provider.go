package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

type Provider struct {
	client *Client
}

func NewProvider(socketPath string) *Provider {
	return &Provider{client: NewClient(socketPath)}
}

func (p *Provider) Type() rt.Type { return rt.TypeDocker }

func (p *Provider) Capabilities() rt.CapabilitySet {
	return rt.NewCapabilitySet(
		rt.CapCreate, rt.CapRemove, rt.CapStart, rt.CapStop, rt.CapRestart,
		rt.CapPause, rt.CapKill, rt.CapLogs, rt.CapMetrics, rt.CapHealth, rt.CapInspect,
		rt.CapExec, rt.CapTerminal, rt.CapConsole,
	)
}

func (p *Provider) Availability(ctx context.Context) rt.Availability {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	version, err := p.client.Ping(checkCtx)
	if err != nil {
		return rt.Availability{Available: false, Message: shortErr(err)}
	}
	return rt.Availability{Available: true, Version: version}
}

func (p *Provider) List(ctx context.Context) ([]rt.Descriptor, error) {
	containers, err := p.client.ListContainers(ctx, true)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]rt.Descriptor, 0, len(containers))
	for _, c := range containers {
		out = append(out, rt.Descriptor{
			ID:        rt.ID(c.ID),
			Type:      rt.TypeDocker,
			Name:      containerName(c.Names),
			Labels:    c.Labels,
			CreatedAt: time.Unix(c.Created, 0),
			Status: rt.Status{
				State:   mapState(c.State),
				Health:  rt.HealthUnknown,
				Message: c.Status,
			},
		})
	}
	return out, nil
}

func (p *Provider) Get(ctx context.Context, id rt.ID) (rt.Runtime, error) {
	if _, err := p.client.InspectContainer(ctx, string(id)); err != nil {
		return nil, wrap(err)
	}
	return &containerRuntime{provider: p, id: id}, nil
}

func (p *Provider) Create(ctx context.Context, spec rt.Spec) (rt.Runtime, error) {
	var cfg CreateConfig
	if err := json.Unmarshal(spec.Config, &cfg); err != nil {
		return nil, fmt.Errorf("%w: docker config: %v", rt.ErrInvalidSpec, err)
	}
	if cfg.Image == "" {
		return nil, fmt.Errorf("%w: image is required", rt.ErrInvalidSpec)
	}
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	for k, v := range spec.Labels {
		cfg.Labels[k] = v
	}
	id, err := p.client.CreateContainer(ctx, spec.Name, cfg)
	if err != nil {
		return nil, wrap(err)
	}
	return &containerRuntime{provider: p, id: rt.ID(id)}, nil
}

func (p *Provider) Remove(ctx context.Context, id rt.ID, opts rt.RemoveOptions) error {
	return wrap(p.client.RemoveContainer(ctx, string(id), opts.Force, opts.Purge))
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func mapState(state string) rt.State {
	switch state {
	case "created":
		return rt.StateCreated
	case "running":
		return rt.StateRunning
	case "paused":
		return rt.StatePaused
	case "restarting":
		return rt.StateStarting
	case "removing":
		return rt.StateStopping
	case "exited":
		return rt.StateStopped
	case "dead":
		return rt.StateFailed
	}
	return rt.StateUnknown
}

func mapHealth(status string) rt.Health {
	switch status {
	case "healthy":
		return rt.HealthHealthy
	case "unhealthy":
		return rt.HealthUnhealthy
	case "starting":
		return rt.HealthStarting
	}
	return rt.HealthUnknown
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	var ae *apiError
	if errAs(err, &ae) {
		switch ae.Status {
		case 404:
			return fmt.Errorf("%w: %s", rt.ErrNotFound, ae.Message)
		case 409:
			return fmt.Errorf("%w: %s", rt.ErrAlreadyExists, ae.Message)
		}
	}
	return err
}

func errAs(err error, target **apiError) bool {
	return errors.As(err, target)
}

func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

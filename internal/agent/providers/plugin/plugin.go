// Package plugin runs third-party runtime providers as external programs.
//
// A plugin is a directory under <dataDir>/plugins/<name>/ containing a
// plugin.json manifest and an executable. The agent speaks a line-delimited
// JSON protocol to it over stdin/stdout:
//
//	→ {"id":1,"method":"list","params":{}}
//	← {"id":1,"result":{"runtimes":[…]}}
//	← {"id":1,"error":{"code":"not_found","message":"…"}}
//
// This mirrors how Terraform and Vault isolate providers: plugins are
// separate processes, so a crashing or hostile plugin cannot corrupt the
// agent, and they may be written in any language. The plugin appears in
// Runix as an ordinary runtime type — nothing above the registry changes.
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

const (
	callTimeout  = 30 * time.Second
	maxLineBytes = 8 << 20
	manifestName = "plugin.json"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// Manifest describes a plugin to the agent.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	RuntimeType string   `json:"runtimeType"`
	Executable  string   `json:"executable"`
	Args        []string `json:"args,omitempty"`
	Description string   `json:"description,omitempty"`
	// Capabilities the plugin claims; unknown names are ignored.
	Capabilities []string `json:"capabilities,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

func (m Manifest) enabled() bool { return m.Enabled == nil || *m.Enabled }

// Discover loads every valid plugin manifest under root.
func Discover(root string) ([]Manifest, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugin: read %s: %w", root, err)
	}
	var out []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), manifestName)) // #nosec G304
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("plugin: %s: invalid manifest: %w", entry.Name(), err)
		}
		if err := m.validate(); err != nil {
			return nil, fmt.Errorf("plugin: %s: %w", entry.Name(), err)
		}
		if !filepath.IsAbs(m.Executable) {
			m.Executable = filepath.Join(root, entry.Name(), m.Executable)
		}
		out = append(out, m)
	}
	return out, nil
}

func (m Manifest) validate() error {
	if !namePattern.MatchString(m.Name) {
		return fmt.Errorf("name must match %s", namePattern)
	}
	if err := rt.Type(m.RuntimeType).Validate(); err != nil {
		return err
	}
	if m.Executable == "" {
		return fmt.Errorf("executable is required")
	}
	return nil
}

// Provider adapts an external plugin to the runtime Provider interface.
type Provider struct {
	manifest Manifest
	caps     rt.CapabilitySet

	mu sync.Mutex // serializes calls: one request in flight per process
}

func NewProvider(m Manifest) *Provider {
	caps := rt.NewCapabilitySet(rt.CapStart, rt.CapStop, rt.CapRestart)
	for _, name := range m.Capabilities {
		switch name {
		case "create":
			caps = caps.With(rt.CapCreate)
		case "remove":
			caps = caps.With(rt.CapRemove)
		case "logs":
			caps = caps.With(rt.CapLogs)
		case "inspect":
			caps = caps.With(rt.CapInspect)
		}
	}
	return &Provider{manifest: m, caps: caps}
}

func (p *Provider) Manifest() Manifest             { return p.manifest }
func (p *Provider) Type() rt.Type                  { return rt.Type(p.manifest.RuntimeType) }
func (p *Provider) Capabilities() rt.CapabilitySet { return p.caps }

func (p *Provider) Availability(ctx context.Context) rt.Availability {
	if !p.manifest.enabled() {
		return rt.Availability{Available: false, Message: "plugin disabled"}
	}
	if _, err := os.Stat(p.manifest.Executable); err != nil {
		return rt.Availability{Available: false, Message: "plugin executable not found"}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := p.call(checkCtx, "ping", nil); err != nil {
		return rt.Availability{Available: false, Message: shortErr(err)}
	}
	return rt.Availability{Available: true, Version: p.manifest.Version}
}

type listResult struct {
	Runtimes []rt.Descriptor `json:"runtimes"`
}

func (p *Provider) List(ctx context.Context) ([]rt.Descriptor, error) {
	raw, err := p.call(ctx, "list", nil)
	if err != nil {
		return nil, err
	}
	var result listResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin %s: decode list: %w", p.manifest.Name, err)
	}
	for i := range result.Runtimes {
		result.Runtimes[i].Type = p.Type()
		if result.Runtimes[i].Status.State == "" {
			result.Runtimes[i].Status.State = rt.StateUnknown
		}
		if result.Runtimes[i].Status.Health == "" {
			result.Runtimes[i].Status.Health = rt.HealthUnknown
		}
	}
	return result.Runtimes, nil
}

func (p *Provider) Get(ctx context.Context, id rt.ID) (rt.Runtime, error) {
	descriptors, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range descriptors {
		if d.ID == id {
			return &pluginRuntime{provider: p, id: id}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s/%s", rt.ErrNotFound, p.Type(), id)
}

func (p *Provider) Create(ctx context.Context, spec rt.Spec) (rt.Runtime, error) {
	if !p.caps.Has(rt.CapCreate) {
		return nil, fmt.Errorf("%w: create on plugin %s", rt.ErrNotSupported, p.manifest.Name)
	}
	raw, err := p.call(ctx, "create", spec)
	if err != nil {
		return nil, err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return nil, fmt.Errorf("plugin %s: create did not return an id", p.manifest.Name)
	}
	return &pluginRuntime{provider: p, id: rt.ID(out.ID)}, nil
}

func (p *Provider) Remove(ctx context.Context, id rt.ID, opts rt.RemoveOptions) error {
	if !p.caps.Has(rt.CapRemove) {
		return fmt.Errorf("%w: remove on plugin %s", rt.ErrNotSupported, p.manifest.Name)
	}
	_, err := p.call(ctx, "remove", map[string]any{"id": id, "force": opts.Force, "purge": opts.Purge})
	return err
}

// request/response envelopes of the stdio protocol.
type request struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *pluginError    `json:"error,omitempty"`
}

type pluginError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *pluginError) toDomain() error {
	switch e.Code {
	case "not_found":
		return fmt.Errorf("%w: %s", rt.ErrNotFound, e.Message)
	case "not_supported":
		return fmt.Errorf("%w: %s", rt.ErrNotSupported, e.Message)
	case "invalid":
		return fmt.Errorf("%w: %s", rt.ErrInvalidSpec, e.Message)
	default:
		return fmt.Errorf("plugin: %s", e.Message)
	}
}

// call runs the plugin for a single request. Spawning per call keeps the
// agent immune to a plugin that hangs or leaks state between operations.
func (p *Provider) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, deadlineSet := ctx.Deadline(); !deadlineSet {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	payload, err := json.Marshal(request{ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, p.manifest.Executable, p.manifest.Args...) // #nosec G204 -- operator-installed plugin
	cmd.Stdin = bytesReader(append(payload, '\n'))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin %s: start: %w", p.manifest.Name, err)
	}

	reader := bufio.NewReaderSize(stdout, 64<<10)
	var resp response
	decodeErr := func() error {
		for {
			line, err := readLimitedLine(reader, maxLineBytes)
			if err != nil {
				return err
			}
			if len(line) == 0 {
				continue
			}
			// Ignore non-JSON chatter so a chatty plugin still works.
			if line[0] != '{' {
				continue
			}
			return json.Unmarshal(line, &resp)
		}
	}()
	_ = cmd.Wait()

	if decodeErr != nil {
		return nil, fmt.Errorf("plugin %s: %s: %w", p.manifest.Name, method, decodeErr)
	}
	if resp.Error != nil {
		return nil, resp.Error.toDomain()
	}
	return resp.Result, nil
}

type pluginRuntime struct {
	provider *Provider
	id       rt.ID
}

func (r *pluginRuntime) ID() rt.ID     { return r.id }
func (r *pluginRuntime) Type() rt.Type { return r.provider.Type() }

func (r *pluginRuntime) Status(ctx context.Context) (rt.Status, error) {
	descriptors, err := r.provider.List(ctx)
	if err != nil {
		return rt.Status{}, err
	}
	for _, d := range descriptors {
		if d.ID == r.id {
			return d.Status, nil
		}
	}
	return rt.Status{}, fmt.Errorf("%w: %s", rt.ErrNotFound, r.id)
}

func (r *pluginRuntime) action(ctx context.Context, action string) error {
	_, err := r.provider.call(ctx, action, map[string]any{"id": r.id})
	return err
}

func (r *pluginRuntime) Start(ctx context.Context) error { return r.action(ctx, "start") }

func (r *pluginRuntime) Stop(ctx context.Context, _ rt.StopOptions) error {
	return r.action(ctx, "stop")
}

func (r *pluginRuntime) Restart(ctx context.Context, _ rt.StopOptions) error {
	return r.action(ctx, "restart")
}

func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 160 {
		return msg[:160]
	}
	return msg
}

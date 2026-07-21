package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	rt "github.com/runix/runix/internal/domain/runtime"
)

type Provider struct {
	dataDir string
	log     *slog.Logger

	mu    sync.Mutex
	procs map[string]*proc
}

// NewProvider loads persisted daemon specs from dataDir and starts the
// autostart ones.
func NewProvider(dataDir string, log *slog.Logger) (*Provider, error) {
	root := filepath.Join(dataDir, "daemons")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("daemon: create data dir: %w", err)
	}
	p := &Provider{dataDir: root, log: log, procs: map[string]*proc{}}
	if err := p.loadAll(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) loadAll() error {
	entries, err := os.ReadDir(p.dataDir)
	if err != nil {
		return fmt.Errorf("daemon: read data dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(p.dataDir, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "spec.json")) // #nosec G304
		if err != nil {
			p.log.Warn("skipping daemon without spec", "dir", entry.Name(), "err", err)
			continue
		}
		var spec Spec
		if err := json.Unmarshal(raw, &spec); err != nil {
			p.log.Warn("skipping daemon with bad spec", "dir", entry.Name(), "err", err)
			continue
		}
		pr := newProc(spec, dir, p.log)
		p.procs[spec.Name] = pr
		if spec.AutoStart {
			if err := pr.start(); err != nil {
				p.log.Error("autostart failed", "daemon", spec.Name, "err", err)
			}
		}
	}
	return nil
}

func (p *Provider) saveSpec(spec Spec) (string, error) {
	dir := filepath.Join(p.dataDir, spec.Name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("daemon: create dir: %w", err)
	}
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), raw, 0o640); err != nil {
		return "", fmt.Errorf("daemon: write spec: %w", err)
	}
	return dir, nil
}

func (p *Provider) Type() rt.Type { return rt.TypeDaemon }

func (p *Provider) Capabilities() rt.CapabilitySet {
	return rt.NewCapabilitySet(
		rt.CapCreate, rt.CapRemove, rt.CapStart, rt.CapStop, rt.CapRestart,
		rt.CapKill, rt.CapLogs, rt.CapMetrics, rt.CapHealth, rt.CapInspect, rt.CapUpdate,
		rt.CapConsole,
	)
}

func (p *Provider) Availability(context.Context) rt.Availability {
	return rt.Availability{Available: true, Version: "builtin"}
}

func (p *Provider) List(context.Context) ([]rt.Descriptor, error) {
	p.mu.Lock()
	names := make([]string, 0, len(p.procs))
	for name := range p.procs {
		names = append(names, name)
	}
	sort.Strings(names)
	descriptors := make([]rt.Descriptor, 0, len(names))
	for _, name := range names {
		pr := p.procs[name]
		descriptors = append(descriptors, rt.Descriptor{
			ID:     rt.ID(name),
			Type:   rt.TypeDaemon,
			Name:   name,
			Labels: map[string]string{"workingDir": pr.workingDir()},
			Status: pr.snapshotStatus(),
		})
	}
	p.mu.Unlock()
	return descriptors, nil
}

func (p *Provider) get(id rt.ID) (*proc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.procs[string(id)]
	if !ok {
		return nil, fmt.Errorf("%w: daemon %s", rt.ErrNotFound, id)
	}
	return pr, nil
}

func (p *Provider) Get(_ context.Context, id rt.ID) (rt.Runtime, error) {
	pr, err := p.get(id)
	if err != nil {
		return nil, err
	}
	return &daemonRuntime{proc: pr}, nil
}

func (p *Provider) Create(_ context.Context, spec rt.Spec) (rt.Runtime, error) {
	var dSpec Spec
	if err := json.Unmarshal(spec.Config, &dSpec); err != nil {
		return nil, fmt.Errorf("%w: daemon config: %v", rt.ErrInvalidSpec, err)
	}
	dSpec.Name = spec.Name
	dSpec.normalize()
	if err := dSpec.Validate(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.procs[dSpec.Name]; exists {
		return nil, fmt.Errorf("%w: daemon %s", rt.ErrAlreadyExists, dSpec.Name)
	}
	dir, err := p.saveSpec(dSpec)
	if err != nil {
		return nil, err
	}
	pr := newProc(dSpec, dir, p.log)
	p.procs[dSpec.Name] = pr
	if dSpec.AutoStart {
		if err := pr.start(); err != nil {
			p.log.Error("autostart on create failed", "daemon", dSpec.Name, "err", err)
		}
	}
	return &daemonRuntime{proc: pr}, nil
}

func (p *Provider) Remove(_ context.Context, id rt.ID, opts rt.RemoveOptions) error {
	pr, err := p.get(id)
	if err != nil {
		return err
	}
	status := pr.snapshotStatus()
	if status.State.IsActive() && !opts.Force {
		return fmt.Errorf("%w: daemon %s is %s (use force)", rt.ErrInvalidTransition, id, status.State)
	}
	_ = pr.stop(false, 0)
	p.mu.Lock()
	delete(p.procs, string(id))
	p.mu.Unlock()
	if opts.Purge {
		return os.RemoveAll(pr.dir)
	}
	// Keep logs, drop only the spec so the daemon does not resurrect.
	return os.Remove(filepath.Join(pr.dir, "spec.json"))
}

// StopAll gracefully stops every running daemon (agent shutdown).
func (p *Provider) StopAll() {
	p.mu.Lock()
	procs := make([]*proc, 0, len(p.procs))
	for _, pr := range p.procs {
		procs = append(procs, pr)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, pr := range procs {
		wg.Add(1)
		go func(pr *proc) {
			defer wg.Done()
			_ = pr.stop(true, 0)
		}(pr)
	}
	wg.Wait()
}

type daemonRuntime struct {
	proc *proc
}

func (d *daemonRuntime) ID() rt.ID     { return rt.ID(d.proc.spec.Name) }
func (d *daemonRuntime) Type() rt.Type { return rt.TypeDaemon }

func (d *daemonRuntime) Status(context.Context) (rt.Status, error) {
	return d.proc.snapshotStatus(), nil
}

func (d *daemonRuntime) Start(context.Context) error {
	return d.proc.start()
}

func (d *daemonRuntime) Stop(_ context.Context, opts rt.StopOptions) error {
	return d.proc.stop(true, opts.Timeout)
}

func (d *daemonRuntime) Restart(_ context.Context, opts rt.StopOptions) error {
	if err := d.proc.stop(true, opts.Timeout); err != nil {
		return err
	}
	return d.proc.start()
}

func (d *daemonRuntime) Kill(_ context.Context, _ string) error {
	return d.proc.stop(false, 0)
}

func (d *daemonRuntime) CheckHealth(context.Context) (rt.Health, error) {
	return d.proc.snapshotStatus().Health, nil
}

func (d *daemonRuntime) Logs(_ context.Context, opts rt.LogOptions) (rt.LogStream, error) {
	return d.proc.buf.stream(opts), nil
}

// Console exposes the supervised process's own stdout/stderr and stdin, so
// console-driven programs can be operated from the UI. Output is taken from
// the live log buffer, which already merges both output streams.
func (d *daemonRuntime) Console(context.Context) (rt.Console, error) {
	if !d.proc.snapshotStatus().State.IsActive() {
		return nil, fmt.Errorf("%w: daemon is not running", rt.ErrInvalidTransition)
	}
	id, ch := d.proc.buf.subscribe()
	return &daemonConsole{proc: d.proc, subID: id, lines: ch}, nil
}

// daemonConsole adapts the log buffer plus the stdin pipe to rt.Console.
type daemonConsole struct {
	proc    *proc
	subID   int
	lines   chan rt.LogEntry
	pending []byte
	closed  chan struct{}
	once    sync.Once
}

func (c *daemonConsole) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		entry, ok := <-c.lines
		if !ok {
			return 0, io.EOF
		}
		c.pending = append(entry.Line, '\n')
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *daemonConsole) Write(p []byte) (int, error) {
	if err := c.proc.writeStdin(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *daemonConsole) Close() error {
	c.once.Do(func() { c.proc.buf.unsubscribe(c.subID) })
	return nil
}

func (d *daemonRuntime) Inspect(context.Context) (json.RawMessage, error) {
	d.proc.mu.Lock()
	doc := struct {
		Spec       Spec      `json:"spec"`
		PID        int       `json:"pid,omitempty"`
		WorkingDir string    `json:"workingDir"`
		Status     rt.Status `json:"status"`
	}{Spec: d.proc.spec, PID: d.proc.pid}
	d.proc.mu.Unlock()
	doc.WorkingDir = d.proc.workingDir()
	doc.Status = d.proc.snapshotStatus()
	return json.Marshal(doc)
}

// Update applies a new spec in place. Name is immutable (it is the daemon's
// identity); a running daemon is restarted so the change takes effect.
func (d *daemonRuntime) Update(_ context.Context, spec rt.Spec) error {
	var dSpec Spec
	if err := json.Unmarshal(spec.Config, &dSpec); err != nil {
		return fmt.Errorf("%w: daemon config: %v", rt.ErrInvalidSpec, err)
	}
	dSpec.Name = d.proc.snapshotSpec().Name
	dSpec.normalize()
	if err := dSpec.Validate(); err != nil {
		return err
	}
	if err := d.proc.persist(dSpec); err != nil {
		return err
	}
	wasActive := d.proc.replaceSpec(dSpec)
	if wasActive {
		if err := d.proc.stop(true, 0); err != nil {
			return err
		}
		return d.proc.start()
	}
	return nil
}

func (d *daemonRuntime) Metrics(ctx context.Context) (rt.Metrics, error) {
	pid := d.proc.currentPID()
	if pid == 0 {
		return rt.Metrics{CollectedAt: time.Now()}, nil
	}
	pr, err := process.NewProcessWithContext(ctx, int32(pid)) // #nosec G115
	if err != nil {
		return rt.Metrics{}, fmt.Errorf("daemon: process %d: %w", pid, err)
	}
	m := rt.Metrics{CollectedAt: time.Now()}
	if cpu, err := pr.CPUPercentWithContext(ctx); err == nil {
		m.CPU.UsagePercent = cpu
	}
	if mem, err := pr.MemoryInfoWithContext(ctx); err == nil && mem != nil {
		m.Memory.UsageBytes = mem.RSS
	}
	if children, err := pr.ChildrenWithContext(ctx); err == nil {
		m.PIDs = uint64(len(children)) + 1
	} else {
		m.PIDs = 1
	}
	return m, nil
}

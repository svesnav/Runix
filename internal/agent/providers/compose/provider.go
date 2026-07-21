// Package compose manages Docker Compose projects through the docker
// compose CLI plugin. Each project is one runtime instance; service-level
// detail is exposed through Inspect.
package compose

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/runix/runix/internal/agent/providers/execx"
	rt "github.com/runix/runix/internal/domain/runtime"
)

var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Provider struct {
	// projectsRoot is where projects created through Runix are written when
	// the caller does not choose a directory.
	projectsRoot string
}

func NewProvider(projectsRoot string) *Provider {
	if projectsRoot == "" {
		projectsRoot = "/opt/runix/compose"
	}
	return &Provider{projectsRoot: projectsRoot}
}

func (p *Provider) Type() rt.Type { return rt.TypeCompose }

func (p *Provider) Capabilities() rt.CapabilitySet {
	return rt.NewCapabilitySet(
		rt.CapCreate, rt.CapStart, rt.CapStop, rt.CapRestart, rt.CapRemove,
		rt.CapLogs, rt.CapInspect, rt.CapUpdate,
	)
}

func (p *Provider) Availability(ctx context.Context) rt.Availability {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := execx.Run(checkCtx, "docker", "compose", "version", "--short")
	if err != nil {
		return rt.Availability{Available: false, Message: "docker compose plugin not available"}
	}
	return rt.Availability{Available: true, Version: strings.TrimSpace(string(out))}
}

type listedProject struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

func (p *Provider) list(ctx context.Context) ([]listedProject, error) {
	out, err := execx.Run(ctx, "docker", "compose", "ls", "-a", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("compose: ls: %w", err)
	}
	var projects []listedProject
	if err := json.Unmarshal(out, &projects); err != nil {
		return nil, fmt.Errorf("compose: parse ls: %w", err)
	}
	return projects, nil
}

func (p *Provider) List(ctx context.Context) ([]rt.Descriptor, error) {
	projects, err := p.list(ctx)
	if err != nil {
		return nil, err
	}
	descriptors := make([]rt.Descriptor, 0, len(projects))
	for _, proj := range projects {
		descriptors = append(descriptors, rt.Descriptor{
			ID:     rt.ID(proj.Name),
			Type:   rt.TypeCompose,
			Name:   proj.Name,
			Labels: map[string]string{"configFiles": proj.ConfigFiles},
			Status: rt.Status{
				State:   mapComposeStatus(proj.Status),
				Health:  rt.HealthUnknown,
				Message: proj.Status,
			},
		})
	}
	return descriptors, nil
}

func (p *Provider) Get(ctx context.Context, id rt.ID) (rt.Runtime, error) {
	name := string(id)
	if !projectNamePattern.MatchString(name) {
		return nil, fmt.Errorf("%w: project name %q", rt.ErrInvalidSpec, name)
	}
	projects, err := p.list(ctx)
	if err != nil {
		return nil, err
	}
	for _, proj := range projects {
		if proj.Name == name {
			return &projectRuntime{name: name, configFiles: splitConfigFiles(proj.ConfigFiles)}, nil
		}
	}
	return nil, fmt.Errorf("%w: compose project %s", rt.ErrNotFound, name)
}

// Config is the compose project document: where the project lives and the
// compose file to write there.
type Config struct {
	// Dir is the project directory; defaults to <ProjectsRoot>/<name>.
	Dir string `json:"dir,omitempty"`
	// Content is the compose YAML. When empty an existing file in Dir is
	// used as-is.
	Content string `json:"content,omitempty"`
	// FileName defaults to compose.yaml.
	FileName string `json:"fileName,omitempty"`
	// Up runs the project immediately after writing.
	Up bool `json:"up"`
}

const defaultComposeFile = "compose.yaml"

// Create writes a compose file and optionally brings the project up. The
// project name is the Runix runtime identity, so it is passed to every
// subsequent compose invocation via -p.
func (p *Provider) Create(ctx context.Context, spec rt.Spec) (rt.Runtime, error) {
	name := spec.Name
	if !projectNamePattern.MatchString(name) {
		return nil, fmt.Errorf("%w: project name must be lowercase alphanumeric, - or _", rt.ErrInvalidSpec)
	}
	var cfg Config
	if len(spec.Config) > 0 {
		if err := json.Unmarshal(spec.Config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: compose config: %v", rt.ErrInvalidSpec, err)
		}
	}
	dir := cfg.Dir
	if dir == "" {
		dir = filepath.Join(p.projectsRoot, name)
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: project dir must be absolute", rt.ErrInvalidSpec)
	}
	fileName := cfg.FileName
	if fileName == "" {
		fileName = defaultComposeFile
	}
	if filepath.Base(fileName) != fileName {
		return nil, fmt.Errorf("%w: file name must not contain a path", rt.ErrInvalidSpec)
	}
	composePath := filepath.Join(dir, fileName)

	if cfg.Content != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("compose: create project dir: %w", err)
		}
		if err := os.WriteFile(composePath, []byte(cfg.Content), 0o644); err != nil {
			return nil, fmt.Errorf("compose: write %s: %w", composePath, err)
		}
	} else if _, err := os.Stat(composePath); err != nil {
		return nil, fmt.Errorf("%w: no compose content given and %s does not exist",
			rt.ErrInvalidSpec, composePath)
	}

	project := &projectRuntime{name: name, configFiles: []string{composePath}}
	if cfg.Up {
		if err := project.Start(ctx); err != nil {
			return nil, err
		}
	}
	return project, nil
}

func (p *Provider) Remove(ctx context.Context, id rt.ID, opts rt.RemoveOptions) error {
	instance, err := p.Get(ctx, id)
	if err != nil {
		return err
	}
	project := instance.(*projectRuntime)
	args := project.composeArgs("down")
	if opts.Purge {
		args = append(args, "--volumes")
	}
	if _, err := execx.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("compose: down %s: %w", id, err)
	}
	return nil
}

// mapComposeStatus interprets strings like "running(3)", "exited(1)",
// "running(2), exited(1)".
func mapComposeStatus(status string) rt.State {
	s := strings.ToLower(status)
	running := strings.Contains(s, "running")
	exited := strings.Contains(s, "exited") || strings.Contains(s, "stopped")
	switch {
	case running && exited:
		return rt.StateDegraded
	case running:
		return rt.StateRunning
	case strings.Contains(s, "paused"):
		return rt.StatePaused
	case strings.Contains(s, "restarting"):
		return rt.StateStarting
	case strings.Contains(s, "created"):
		return rt.StateCreated
	case exited:
		return rt.StateStopped
	}
	return rt.StateUnknown
}

func splitConfigFiles(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type projectRuntime struct {
	name        string
	configFiles []string
}

func (r *projectRuntime) ID() rt.ID     { return rt.ID(r.name) }
func (r *projectRuntime) Type() rt.Type { return rt.TypeCompose }

func (r *projectRuntime) composeArgs(verb string, extra ...string) []string {
	args := []string{"compose", "-p", r.name}
	for _, f := range r.configFiles {
		args = append(args, "-f", f)
	}
	args = append(args, verb)
	return append(args, extra...)
}

func (r *projectRuntime) Status(ctx context.Context) (rt.Status, error) {
	out, err := execx.Run(ctx, "docker", "compose", "ls", "-a", "--format", "json")
	if err != nil {
		return rt.Status{}, fmt.Errorf("compose: ls: %w", err)
	}
	var projects []listedProject
	if err := json.Unmarshal(out, &projects); err != nil {
		return rt.Status{}, err
	}
	for _, proj := range projects {
		if proj.Name == r.name {
			return rt.Status{
				State:   mapComposeStatus(proj.Status),
				Health:  rt.HealthUnknown,
				Message: proj.Status,
			}, nil
		}
	}
	return rt.Status{}, fmt.Errorf("%w: compose project %s", rt.ErrNotFound, r.name)
}

func (r *projectRuntime) Start(ctx context.Context) error {
	_, err := execx.Run(ctx, "docker", r.composeArgs("up", "-d")...)
	if err != nil {
		return fmt.Errorf("compose: up %s: %w", r.name, err)
	}
	return nil
}

func (r *projectRuntime) Stop(ctx context.Context, opts rt.StopOptions) error {
	args := r.composeArgs("stop")
	if opts.Timeout > 0 {
		args = append(args, "-t", strconv.Itoa(int(opts.Timeout.Seconds())))
	}
	if _, err := execx.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("compose: stop %s: %w", r.name, err)
	}
	return nil
}

func (r *projectRuntime) Restart(ctx context.Context, opts rt.StopOptions) error {
	args := r.composeArgs("restart")
	if opts.Timeout > 0 {
		args = append(args, "-t", strconv.Itoa(int(opts.Timeout.Seconds())))
	}
	if _, err := execx.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("compose: restart %s: %w", r.name, err)
	}
	return nil
}

// Update rewrites the project's compose file and recreates it so the new
// definition takes effect.
func (r *projectRuntime) Update(ctx context.Context, spec rt.Spec) error {
	var cfg Config
	if err := json.Unmarshal(spec.Config, &cfg); err != nil {
		return fmt.Errorf("%w: compose config: %v", rt.ErrInvalidSpec, err)
	}
	if cfg.Content == "" {
		return fmt.Errorf("%w: compose content is required", rt.ErrInvalidSpec)
	}
	if len(r.configFiles) == 0 {
		return fmt.Errorf("%w: project %s has no known compose file", rt.ErrInvalidSpec, r.name)
	}
	target := r.configFiles[0]
	if err := os.WriteFile(target, []byte(cfg.Content), 0o644); err != nil {
		return fmt.Errorf("compose: write %s: %w", target, err)
	}
	// up -d reconciles the running project with the new file.
	return r.Start(ctx)
}

// ComposeFile returns the project's primary compose file path so the API can
// serve its contents for editing.
func (r *projectRuntime) ComposeFile() string {
	if len(r.configFiles) == 0 {
		return ""
	}
	return r.configFiles[0]
}

// Inspect returns the project's services plus its compose file, so the UI
// can both show status and prefill the editor.
func (r *projectRuntime) Inspect(ctx context.Context) (json.RawMessage, error) {
	out, err := execx.Run(ctx, "docker", r.composeArgs("ps", "-a", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("compose: ps %s: %w", r.name, err)
	}
	// docker compose ps emits NDJSON; wrap into an array.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	services := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			services = append(services, json.RawMessage(line))
		}
	}

	doc := struct {
		Project     string            `json:"project"`
		ComposeFile string            `json:"composeFile,omitempty"`
		Content     string            `json:"content,omitempty"`
		Services    []json.RawMessage `json:"services"`
	}{Project: r.name, ComposeFile: r.ComposeFile(), Services: services}

	if path := r.ComposeFile(); path != "" {
		// Best effort: the file may live somewhere the agent cannot read.
		if raw, err := os.ReadFile(path); err == nil && len(raw) < 1<<20 { // #nosec G304
			doc.Content = string(raw)
		}
	}
	return json.Marshal(doc)
}

func (r *projectRuntime) Logs(ctx context.Context, opts rt.LogOptions) (rt.LogStream, error) {
	args := r.composeArgs("logs", "--no-color")
	if opts.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(opts.Tail))
	} else {
		args = append(args, "--tail", "200")
	}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Timestamps {
		args = append(args, "-t")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	pipe, wait, err := execx.Stream(streamCtx, "docker", args...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("compose: logs %s: %w", r.name, err)
	}
	return &composeLogStream{
		pipe:    pipe,
		scanner: bufio.NewScanner(pipe),
		wait:    wait,
		cancel:  cancel,
	}, nil
}

type composeLogStream struct {
	pipe    io.ReadCloser
	scanner *bufio.Scanner
	wait    func() error
	cancel  context.CancelFunc
}

func (s *composeLogStream) Next(ctx context.Context) (rt.LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return rt.LogEntry{}, err
	}
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil && ctx.Err() == nil {
			return rt.LogEntry{}, err
		}
		return rt.LogEntry{}, io.EOF
	}
	return rt.LogEntry{
		Source: rt.LogSourceStdout,
		Line:   append([]byte{}, s.scanner.Bytes()...),
	}, nil
}

func (s *composeLogStream) Close() error {
	s.cancel()
	_ = s.pipe.Close()
	_ = s.wait()
	return nil
}

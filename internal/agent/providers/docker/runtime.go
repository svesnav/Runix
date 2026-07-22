package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

type containerRuntime struct {
	provider *Provider
	id       rt.ID
}

func (c *containerRuntime) ID() rt.ID     { return c.id }
func (c *containerRuntime) Type() rt.Type { return rt.TypeDocker }

func (c *containerRuntime) Status(ctx context.Context) (rt.Status, error) {
	detail, err := c.provider.client.InspectContainer(ctx, string(c.id))
	if err != nil {
		return rt.Status{}, wrap(err)
	}
	status := rt.Status{
		State:        mapState(detail.State.Status),
		Health:       rt.HealthUnknown,
		RestartCount: detail.RestartCount,
	}
	if detail.State.Health != nil {
		status.Health = mapHealth(detail.State.Health.Status)
	}
	if !detail.State.Running && detail.State.Status == "exited" {
		code := detail.State.ExitCode
		status.ExitCode = &code
	}
	if t, err := time.Parse(time.RFC3339Nano, detail.State.StartedAt); err == nil && !t.IsZero() && t.Year() > 1 {
		status.StartedAt = &t
	}
	if t, err := time.Parse(time.RFC3339Nano, detail.State.FinishedAt); err == nil && t.Year() > 1 {
		status.FinishedAt = &t
	}
	return status, nil
}

func (c *containerRuntime) Start(ctx context.Context) error {
	return wrap(c.provider.client.StartContainer(ctx, string(c.id)))
}

// Stop asks the container to shut itself down the way its software expects
// before falling back to Docker's own stop (the configured stop signal,
// then SIGKILL after the timeout).
func (c *containerRuntime) Stop(ctx context.Context, opts rt.StopOptions) error {
	if cmd := c.stopCommand(ctx); cmd != "" {
		if c.sendStopCommand(ctx, cmd, opts.Timeout) {
			return nil
		}
	}
	return wrap(c.provider.client.StopContainer(ctx, string(c.id), opts.Timeout))
}

// stopCommand reads the command recorded on the container at create time.
func (c *containerRuntime) stopCommand(ctx context.Context) string {
	detail, err := c.provider.client.InspectContainer(ctx, string(c.id))
	if err != nil {
		return ""
	}
	return detail.Config.Labels[StopCommandLabel]
}

// sendStopCommand writes the command to the container's console and waits
// for it to exit. Reports whether the container actually stopped, so the
// caller can fall back rather than leave it running.
func (c *containerRuntime) sendStopCommand(ctx context.Context, cmd string, timeout time.Duration) bool {
	console, err := c.Console(ctx)
	if err != nil {
		return false
	}
	defer console.Close()

	if _, err := console.Write([]byte(cmd + "\n")); err != nil {
		return false
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := c.Status(ctx)
		if err == nil && !status.State.IsActive() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false
}

func (c *containerRuntime) Restart(ctx context.Context, opts rt.StopOptions) error {
	return wrap(c.provider.client.RestartContainer(ctx, string(c.id), opts.Timeout))
}

func (c *containerRuntime) Pause(ctx context.Context) error {
	return wrap(c.provider.client.PauseContainer(ctx, string(c.id)))
}

func (c *containerRuntime) Resume(ctx context.Context) error {
	return wrap(c.provider.client.UnpauseContainer(ctx, string(c.id)))
}

func (c *containerRuntime) Kill(ctx context.Context, signal string) error {
	return wrap(c.provider.client.KillContainer(ctx, string(c.id), signal))
}

// Exec runs a one-shot command inside the container and reports its exit
// code. Output is demultiplexed into the caller's writers.
func (c *containerRuntime) Exec(ctx context.Context, spec rt.ExecSpec) (rt.ExecResult, error) {
	if len(spec.Cmd) == 0 {
		return rt.ExecResult{}, fmt.Errorf("%w: cmd is required", rt.ErrInvalidSpec)
	}
	execID, err := c.provider.client.createExec(ctx, string(c.id), execConfig{
		AttachStdin:  spec.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          spec.TTY,
		Cmd:          spec.Cmd,
		Env:          spec.Env,
		WorkingDir:   spec.WorkingDir,
		User:         spec.User,
	})
	if err != nil {
		return rt.ExecResult{}, wrap(err)
	}
	conn, err := c.provider.client.startExec(ctx, execID, spec.TTY)
	if err != nil {
		return rt.ExecResult{}, wrap(err)
	}
	defer conn.Close()

	if spec.Stdin != nil {
		go func() {
			_, _ = io.Copy(conn, spec.Stdin)
			_ = conn.closeWrite()
		}()
	}
	if spec.TTY {
		// With a TTY there is no stream framing: everything is stdout.
		if _, err := io.Copy(orDiscard(spec.Stdout), conn); err != nil {
			return rt.ExecResult{}, err
		}
	} else if err := demux(conn, spec.Stdout, spec.Stderr); err != nil {
		return rt.ExecResult{}, err
	}

	code, err := c.provider.client.execExitCode(ctx, execID)
	if err != nil {
		return rt.ExecResult{}, wrap(err)
	}
	return rt.ExecResult{ExitCode: code}, nil
}

// Terminal opens an interactive shell inside the container. The command
// falls back through common shells so both distro and scratch-ish images
// work.
func (c *containerRuntime) Terminal(ctx context.Context, spec rt.TerminalSpec) (rt.Terminal, error) {
	cmd := spec.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "exec $(command -v bash || command -v sh) -l 2>/dev/null || exec /bin/sh"}
	}
	env := append([]string{"TERM=xterm-256color"}, spec.Env...)
	execID, err := c.provider.client.createExec(ctx, string(c.id), execConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
		Env:          env,
		User:         spec.User,
	})
	if err != nil {
		return nil, wrap(err)
	}
	// The stream outlives this call, so it must not inherit the request
	// context's cancellation.
	conn, err := c.provider.client.startExec(context.WithoutCancel(ctx), execID, true)
	if err != nil {
		return nil, wrap(err)
	}
	term := &execTerminal{client: c.provider.client, execID: execID, conn: conn}
	if spec.Cols > 0 && spec.Rows > 0 {
		_ = term.Resize(spec.Cols, spec.Rows)
	}
	return term, nil
}

// Console attaches to the container's main process. The container must have
// been created with stdin open (docker run -i); otherwise input has nowhere
// to go and we say so rather than silently dropping keystrokes.
func (c *containerRuntime) Console(ctx context.Context) (rt.Console, error) {
	detail, err := c.provider.client.InspectContainer(ctx, string(c.id))
	if err != nil {
		return nil, wrap(err)
	}
	if !detail.Config.OpenStdin {
		return nil, fmt.Errorf(
			"%w: this container was created without an input stream; recreate it with stdin enabled to use the console",
			rt.ErrNotSupported)
	}
	conn, err := c.provider.client.attach(context.WithoutCancel(ctx), string(c.id))
	if err != nil {
		return nil, wrap(err)
	}
	return &containerConsole{conn: conn, tty: detail.Config.Tty}, nil
}

// containerConsole demultiplexes attach output when the container has no
// TTY, and passes bytes straight through when it does.
type containerConsole struct {
	conn    *hijackedConn
	tty     bool
	demuxed io.Reader
	once    sync.Once
}

func (c *containerConsole) Read(p []byte) (int, error) {
	if c.tty {
		return c.conn.Read(p)
	}
	if c.demuxed == nil {
		pr, pw := io.Pipe()
		go func() {
			// stderr is folded into the same view: a console shows both.
			_ = pw.CloseWithError(demux(c.conn, pw, pw))
		}()
		c.demuxed = pr
	}
	return c.demuxed.Read(p)
}

func (c *containerConsole) Write(p []byte) (int, error) { return c.conn.Write(p) }

func (c *containerConsole) Close() error {
	var err error
	c.once.Do(func() { err = c.conn.Close() })
	return err
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func (c *containerRuntime) CheckHealth(ctx context.Context) (rt.Health, error) {
	status, err := c.Status(ctx)
	if err != nil {
		return rt.HealthUnknown, err
	}
	return status.Health, nil
}

func (c *containerRuntime) Inspect(ctx context.Context) (json.RawMessage, error) {
	raw, err := c.provider.client.InspectRaw(ctx, string(c.id))
	return raw, wrap(err)
}

func (c *containerRuntime) Metrics(ctx context.Context) (rt.Metrics, error) {
	s, err := c.provider.client.Stats(ctx, string(c.id))
	if err != nil {
		return rt.Metrics{}, wrap(err)
	}
	m := rt.Metrics{
		CollectedAt: time.Now(),
		Memory: rt.MemoryMetrics{
			UsageBytes: s.MemoryStats.Usage,
			LimitBytes: s.MemoryStats.Limit,
		},
		PIDs: s.PidsStats.Current,
	}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		cpus := float64(s.CPUStats.OnlineCPUs)
		if cpus == 0 {
			cpus = 1
		}
		m.CPU.UsagePercent = cpuDelta / sysDelta * cpus * 100
	}
	m.CPU.ThrottledPeriods = s.CPUStats.Throttling.ThrottledPeriods
	for _, n := range s.Networks {
		m.Network.RxBytes += n.RxBytes
		m.Network.TxBytes += n.TxBytes
	}
	for _, io := range s.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(io.Op) {
		case "read":
			m.BlockIO.ReadBytes += io.Value
		case "write":
			m.BlockIO.WriteBytes += io.Value
		}
	}
	return m, nil
}

func (c *containerRuntime) Logs(ctx context.Context, opts rt.LogOptions) (rt.LogStream, error) {
	detail, err := c.provider.client.InspectContainer(ctx, string(c.id))
	if err != nil {
		return nil, wrap(err)
	}
	body, err := c.provider.client.Logs(ctx, string(c.id), opts.Follow, opts.Tail, opts.Timestamps)
	if err != nil {
		return nil, wrap(err)
	}
	stream := &logStream{body: body, tty: detail.Config.Tty, reader: bufio.NewReader(body)}
	// Cancel blocks in-flight reads by closing the body.
	go func() {
		<-ctx.Done()
		_ = body.Close()
	}()
	return stream, nil
}

// logStream demultiplexes the engine's 8-byte-header stdout/stderr framing
// (plain lines for TTY containers).
type logStream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	tty    bool

	frameLeft int
	source    rt.LogSource
}

func (l *logStream) Next(ctx context.Context) (rt.LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return rt.LogEntry{}, err
	}
	if l.tty {
		line, err := l.reader.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return rt.LogEntry{}, mapReadErr(err)
		}
		return parseLine(rt.LogSourceStdout, line), nil
	}
	for l.frameLeft == 0 {
		var header [8]byte
		if _, err := io.ReadFull(l.reader, header[:]); err != nil {
			return rt.LogEntry{}, mapReadErr(err)
		}
		switch header[0] {
		case 2:
			l.source = rt.LogSourceStderr
		default:
			l.source = rt.LogSourceStdout
		}
		l.frameLeft = int(binary.BigEndian.Uint32(header[4:]))
	}
	line, err := l.reader.ReadBytes('\n')
	l.frameLeft -= len(line)
	if l.frameLeft < 0 {
		l.frameLeft = 0
	}
	if len(line) == 0 && err != nil {
		return rt.LogEntry{}, mapReadErr(err)
	}
	return parseLine(l.source, line), nil
}

func (l *logStream) Close() error {
	return l.body.Close()
}

func mapReadErr(err error) error {
	if strings.Contains(err.Error(), "closed") {
		return io.EOF
	}
	return err
}

// parseLine splits the optional RFC3339Nano timestamp Docker prepends when
// timestamps are requested.
func parseLine(source rt.LogSource, raw []byte) rt.LogEntry {
	line := strings.TrimRight(string(raw), "\r\n")
	entry := rt.LogEntry{Source: source, Line: []byte(line)}
	if idx := strings.IndexByte(line, ' '); idx > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, line[:idx]); err == nil {
			entry.Timestamp = ts
			entry.Line = []byte(line[idx+1:])
		}
	}
	return entry
}

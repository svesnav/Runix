package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
)

const logFileMaxBytes = 10 << 20

// proc is one supervised daemon: its spec, its current process (if any) and
// the supervision loop deciding on restarts.
type proc struct {
	dir string
	buf *logBuffer
	log *slog.Logger

	mu    sync.Mutex
	spec  Spec
	state rt.State
	cmd   *exec.Cmd
	// stdin stays open for the life of the process so operators can drive
	// console-driven programs (a game server's "stop", "say hello", …).
	stdin      io.WriteCloser
	pid        int
	exitCode   *int
	startedAt  *time.Time
	finishedAt *time.Time
	restarts   int
	desired    bool          // operator wants it running
	generation int           // bumped on every manual stop/start to cancel stale loops
	loopDone   chan struct{} // closed when the supervise loop exits
}

func newProc(spec Spec, dir string, log *slog.Logger) *proc {
	spec.normalize()
	return &proc{
		dir:   dir,
		buf:   newLogBuffer(),
		log:   log.With("daemon", spec.Name),
		spec:  spec,
		state: rt.StateCreated,
	}
}

func (p *proc) snapshotStatus() rt.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return rt.Status{
		State:        p.state,
		Health:       healthFor(p.state),
		ExitCode:     p.exitCode,
		StartedAt:    p.startedAt,
		FinishedAt:   p.finishedAt,
		RestartCount: p.restarts,
	}
}

func (p *proc) snapshotSpec() Spec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spec
}

// workingDir is where the daemon runs and where its file manager opens. It
// defaults to the daemon's own state directory when unset.
func (p *proc) workingDir() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.spec.WorkingDir != "" {
		return p.spec.WorkingDir
	}
	return p.dir
}

// replaceSpec swaps the running configuration. The caller has already
// validated and persisted the new spec. Returns whether the daemon was
// active, so callers can decide to restart.
func (p *proc) replaceSpec(spec Spec) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.spec = spec
	return p.state.IsActive()
}

// writeStdin sends input to the running process. The pipe is replaced on
// every restart, so it is read under the lock rather than cached.
func (p *proc) writeStdin(data []byte) error {
	p.mu.Lock()
	stdin := p.stdin
	// Any live state, not just Running: a graceful stop moves the process
	// to Stopping and *then* writes its stop command, so demanding Running
	// here would reject the very write the shutdown depends on.
	alive := p.state.IsActive()
	p.mu.Unlock()
	if stdin == nil || !alive {
		return fmt.Errorf("%w: daemon is not running", rt.ErrInvalidTransition)
	}
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("daemon: write to stdin: %w", err)
	}
	return nil
}

// persist writes the spec to the daemon's state directory so it survives an
// agent restart.
func (p *proc) persist(spec Spec) error {
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p.dir, "spec.json"), raw, 0o640); err != nil {
		return fmt.Errorf("daemon: write spec: %w", err)
	}
	return nil
}

func healthFor(s rt.State) rt.Health {
	switch s {
	case rt.StateRunning:
		return rt.HealthHealthy
	case rt.StateFailed:
		return rt.HealthUnhealthy
	}
	return rt.HealthUnknown
}

func (p *proc) currentPID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// start launches the supervision loop if the daemon is not already active.
func (p *proc) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.IsActive() {
		return fmt.Errorf("%w: daemon %s is already %s", rt.ErrInvalidTransition, p.spec.Name, p.state)
	}
	p.desired = true
	p.restarts = 0
	p.exitCode = nil
	p.state = rt.StateStarting
	p.generation++
	gen := p.generation
	p.loopDone = make(chan struct{})
	go p.superviseLoop(gen, p.loopDone)
	return nil
}

func (p *proc) superviseLoop(gen int, done chan struct{}) {
	defer close(done)
	delay := p.spec.restartDelay()
	for {
		err := p.runOnce(gen)
		p.mu.Lock()
		if p.generation != gen || !p.desired {
			// Manual stop already set the final state.
			p.mu.Unlock()
			return
		}
		exitedClean := p.exitCode != nil && *p.exitCode == 0
		policy := p.spec.RestartPolicy
		maxRestarts := p.spec.MaxRestarts
		restarts := p.restarts
		p.mu.Unlock()

		if err != nil {
			p.buf.append(rt.LogSourceSystem, []byte("start failed: "+err.Error()))
		}

		stop := false
		final := rt.StateStopped
		switch {
		case policy == RestartNever:
			stop = true
			if !exitedClean {
				final = rt.StateFailed
			}
		case policy == RestartOnFailure && exitedClean:
			stop = true
		case maxRestarts > 0 && restarts >= maxRestarts:
			stop = true
			final = rt.StateFailed
			p.buf.append(rt.LogSourceSystem,
				[]byte(fmt.Sprintf("giving up after %d restarts", restarts)))
		}
		if stop {
			p.mu.Lock()
			if p.generation == gen {
				p.state = final
				p.desired = false
			}
			p.mu.Unlock()
			return
		}

		p.buf.append(rt.LogSourceSystem,
			[]byte(fmt.Sprintf("restarting in %s (attempt %d)", delay, restarts+1)))
		p.mu.Lock()
		p.state = rt.StateStarting
		p.mu.Unlock()

		timer := time.NewTimer(delay)
		<-timer.C
		p.mu.Lock()
		canceled := p.generation != gen || !p.desired
		p.restarts++
		p.mu.Unlock()
		if canceled {
			return
		}
		delay *= 2
		if maxDelay := p.spec.maxRestartDelay(); delay > maxDelay {
			delay = maxDelay
		}
	}
}

// runOnce launches the process and blocks until it exits.
func (p *proc) runOnce(gen int) error {
	p.mu.Lock()
	spec := p.spec
	p.mu.Unlock()

	cmd := exec.Command(spec.Cmd[0], spec.Cmd[1:]...) // #nosec G204 -- running operator-defined commands is this feature
	cmd.Dir = spec.WorkingDir
	if cmd.Dir == "" {
		cmd.Dir = p.dir
	}
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setProcAttrs(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	logFile := p.openLogFile()
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		if logFile != nil {
			_ = logFile.Close()
		}
		p.mu.Lock()
		code := -1
		p.exitCode = &code
		p.mu.Unlock()
		return err
	}

	now := time.Now()
	p.mu.Lock()
	if p.generation != gen {
		p.mu.Unlock()
		_ = cmd.Process.Kill()
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil
	}
	p.cmd = cmd
	p.stdin = stdin
	p.pid = cmd.Process.Pid
	p.startedAt = &now
	p.finishedAt = nil
	p.state = rt.StateRunning
	p.mu.Unlock()
	p.buf.append(rt.LogSourceSystem, []byte(fmt.Sprintf("started pid %d", cmd.Process.Pid)))

	var wg sync.WaitGroup
	wg.Add(2)
	go p.pipeOutput(&wg, stdout, rt.LogSourceStdout, logFile)
	go p.pipeOutput(&wg, stderr, rt.LogSourceStderr, logFile)
	wg.Wait()
	err = cmd.Wait()
	if logFile != nil {
		_ = logFile.Close()
	}

	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		code = -1
	}
	finished := time.Now()
	p.mu.Lock()
	p.cmd = nil
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	p.pid = 0
	p.exitCode = &code
	p.finishedAt = &finished
	p.mu.Unlock()
	p.buf.append(rt.LogSourceSystem, []byte(fmt.Sprintf("exited with code %d", code)))
	return nil
}

func (p *proc) pipeOutput(wg *sync.WaitGroup, r io.Reader, source rt.LogSource, file *os.File) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		p.buf.append(source, line)
		if file != nil {
			_, _ = file.Write(append(append([]byte(time.Now().Format(time.RFC3339)+" "), line...), '\n'))
		}
	}
}

// openLogFile opens the append-only log, rotating once past the size cap.
func (p *proc) openLogFile() *os.File {
	path := filepath.Join(p.dir, "daemon.log")
	if info, err := os.Stat(path); err == nil && info.Size() > logFileMaxBytes {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640) // #nosec G304
	if err != nil {
		p.log.Warn("cannot open daemon log file", "err", err)
		return nil
	}
	return f
}

// stop ends the daemon: graceful signal, then kill after the timeout.
func (p *proc) stop(graceful bool, timeout time.Duration) error {
	p.mu.Lock()
	if !p.state.IsActive() {
		p.mu.Unlock()
		return nil
	}
	p.desired = false
	p.generation++
	p.state = rt.StateStopping
	cmd := p.cmd
	loopDone := p.loopDone
	spec := p.spec
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if graceful {
			if timeout <= 0 {
				timeout = spec.stopTimeout()
			}
			// A configured stop command gets the first and longest chance:
			// it is the only shutdown the program itself considers clean.
			// The signal below remains the fallback, so a process that
			// ignores the command is still stopped rather than left running.
			if spec.StopCommand != "" && p.writeStdin([]byte(spec.StopCommand+"\n")) == nil {
				p.buf.append(rt.LogSourceSystem, []byte("sent stop command: "+spec.StopCommand))
				select {
				case <-loopDone:
					p.mu.Lock()
					p.state = rt.StateStopped
					p.mu.Unlock()
					return nil
				case <-time.After(timeout):
					p.buf.append(rt.LogSourceSystem,
						[]byte("stop command did not finish in time, signalling"))
				}
			}
			_ = signalProcess(cmd, spec.StopSignal)
			select {
			case <-loopDone:
			case <-time.After(timeout):
				p.buf.append(rt.LogSourceSystem, []byte("stop timeout exceeded, killing"))
				_ = killProcess(cmd)
				<-loopDone
			}
		} else {
			_ = killProcess(cmd)
			<-loopDone
		}
	} else if loopDone != nil {
		select {
		case <-loopDone:
		case <-time.After(time.Second):
		}
	}

	p.mu.Lock()
	p.state = rt.StateStopped
	p.mu.Unlock()
	return nil
}

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

const execOutputLimit = 1 << 20

func registerCoreHandlers(reg *rpcRegistry, registry *rt.Registry) {
	reg.call(protocol.MethodPing, func(context.Context, json.RawMessage) (any, *protocol.Error) {
		return map[string]string{"pong": time.Now().UTC().Format(time.RFC3339)}, nil
	})

	reg.call(protocol.MethodRuntimeList, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeListParams](raw)
		if e != nil {
			return nil, e
		}
		providers := registry.Providers()
		result := protocol.RuntimeListResult{Runtimes: []protocol.RuntimeInfo{}}
		for _, p := range providers {
			if params.Type != "" && string(p.Type()) != params.Type {
				continue
			}
			if !p.Availability(ctx).Available {
				continue
			}
			descriptors, err := p.List(ctx)
			if err != nil {
				return nil, perr(fmt.Errorf("provider %s: %w", p.Type(), err))
			}
			caps := p.Capabilities().Strings()
			for _, d := range descriptors {
				result.Runtimes = append(result.Runtimes, protocol.RuntimeInfo{
					Descriptor: d, Capabilities: caps,
				})
			}
		}
		return result, nil
	})

	reg.call(protocol.MethodRuntimeGet, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeGetParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		descriptors, err := provider.List(ctx)
		if err != nil {
			return nil, perr(err)
		}
		// Providers accept aliases (docker: short IDs and names), so match
		// exact ID, then name, then ID prefix.
		match := -1
		for i, d := range descriptors {
			switch {
			case string(d.ID) == params.ID:
				match = i
			case match == -1 && d.Name == params.ID:
				match = i
			case match == -1 && len(params.ID) >= 12 && strings.HasPrefix(string(d.ID), params.ID):
				match = i
			}
			if string(d.ID) == params.ID {
				break
			}
		}
		if match >= 0 {
			return protocol.RuntimeInfo{
				Descriptor: descriptors[match], Capabilities: provider.Capabilities().Strings(),
			}, nil
		}
		return nil, perr(fmt.Errorf("%w: %s/%s", rt.ErrNotFound, params.Type, params.ID))
	})

	reg.call(protocol.MethodRuntimeAction, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeActionParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		instance, err := provider.Get(ctx, rt.ID(params.ID))
		if err != nil {
			return nil, perr(err)
		}
		return nil, perr(applyAction(ctx, instance, params))
	})

	reg.call(protocol.MethodRuntimeCreate, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeCreateParams](raw)
		if e != nil {
			return nil, e
		}
		if err := params.Spec.Validate(); err != nil {
			return nil, perr(err)
		}
		provider, err := registry.Get(params.Spec.Type)
		if err != nil {
			return nil, perr(err)
		}
		instance, err := provider.Create(ctx, params.Spec)
		if err != nil {
			return nil, perr(err)
		}
		return map[string]string{"id": string(instance.ID())}, nil
	})

	reg.call(protocol.MethodRuntimeUpdate, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeUpdateParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		instance, err := provider.Get(ctx, rt.ID(params.ID))
		if err != nil {
			return nil, perr(err)
		}
		updater, ok := instance.(rt.Updater)
		if !ok {
			return nil, perr(fmt.Errorf("%w: update on %s", rt.ErrNotSupported, params.Type))
		}
		return nil, perr(updater.Update(ctx, params.Spec))
	})

	reg.call(protocol.MethodRuntimeInspect, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeInspectParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		instance, err := provider.Get(ctx, rt.ID(params.ID))
		if err != nil {
			return nil, perr(err)
		}
		inspector, ok := instance.(rt.Inspector)
		if !ok {
			return nil, perr(fmt.Errorf("%w: inspect on %s", rt.ErrNotSupported, params.Type))
		}
		doc, err := inspector.Inspect(ctx)
		if err != nil {
			return nil, perr(err)
		}
		return json.RawMessage(doc), nil
	})

	reg.call(protocol.MethodRuntimeRemove, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeRemoveParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		return nil, perr(provider.Remove(ctx, rt.ID(params.ID), rt.RemoveOptions{
			Force: params.Force, Purge: params.Purge,
		}))
	})

	reg.call(protocol.MethodRuntimeExec, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.RuntimeExecParams](raw)
		if e != nil {
			return nil, e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return nil, perr(err)
		}
		instance, err := provider.Get(ctx, rt.ID(params.ID))
		if err != nil {
			return nil, perr(err)
		}
		execer, ok := instance.(rt.Execer)
		if !ok {
			return nil, perr(fmt.Errorf("%w: exec on %s", rt.ErrNotSupported, params.Type))
		}
		if params.TimeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(params.TimeoutSeconds)*time.Second)
			defer cancel()
		}
		var stdout, stderr limitedBuffer
		stdout.limit, stderr.limit = execOutputLimit, execOutputLimit
		res, err := execer.Exec(ctx, rt.ExecSpec{
			Cmd: params.Cmd, Env: params.Env, WorkingDir: params.WorkingDir,
			User: params.User, Stdout: &stdout, Stderr: &stderr,
		})
		if err != nil {
			return nil, perr(err)
		}
		return protocol.RuntimeExecResult{
			ExitCode:  res.ExitCode,
			Stdout:    stdout.Bytes(),
			Stderr:    stderr.Bytes(),
			Truncated: stdout.truncated || stderr.truncated,
		}, nil
	})

	reg.stream(protocol.MethodRuntimeLogs, func(ctx context.Context, raw json.RawMessage, st *AgentStream) *protocol.Error {
		params, e := decodeParams[protocol.RuntimeLogsParams](raw)
		if e != nil {
			return e
		}
		provider, err := registry.Get(rt.Type(params.Type))
		if err != nil {
			return perr(err)
		}
		instance, err := provider.Get(ctx, rt.ID(params.ID))
		if err != nil {
			return perr(err)
		}
		streamer, ok := instance.(rt.LogStreamer)
		if !ok {
			return perr(fmt.Errorf("%w: logs on %s", rt.ErrNotSupported, params.Type))
		}
		logs, err := streamer.Logs(ctx, rt.LogOptions{
			Follow: params.Follow, Tail: params.Tail, Timestamps: params.Timestamps,
		})
		if err != nil {
			return perr(err)
		}
		defer logs.Close()
		for {
			entry, err := logs.Next(ctx)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil // stream canceled by the server side
				}
				return perr(err)
			}
			if err := st.SendJSON(protocol.LogLine{
				Timestamp: entry.Timestamp,
				Source:    string(entry.Source),
				Line:      string(entry.Line),
			}); err != nil {
				return nil
			}
		}
	})
}

func applyAction(ctx context.Context, instance rt.Runtime, params protocol.RuntimeActionParams) error {
	switch params.Action {
	case protocol.ActionStart:
		return instance.Start(ctx)
	case protocol.ActionStop:
		return instance.Stop(ctx, params.Stop)
	case protocol.ActionRestart:
		return instance.Restart(ctx, params.Stop)
	case protocol.ActionPause:
		if p, ok := instance.(rt.Pauser); ok {
			return p.Pause(ctx)
		}
	case protocol.ActionResume:
		if p, ok := instance.(rt.Pauser); ok {
			return p.Resume(ctx)
		}
	case protocol.ActionKill:
		if k, ok := instance.(rt.Killer); ok {
			signal := params.Signal
			if signal == "" {
				signal = "SIGKILL"
			}
			return k.Kill(ctx, signal)
		}
	case protocol.ActionReload:
		if r, ok := instance.(rt.Reloader); ok {
			return r.Reload(ctx)
		}
	default:
		if inv, ok := instance.(rt.ActionInvoker); ok {
			return inv.InvokeAction(ctx, params.Action)
		}
		return fmt.Errorf("%w: action %q", rt.ErrNotSupported, params.Action)
	}
	return fmt.Errorf("%w: action %q on %s", rt.ErrNotSupported, params.Action, instance.Type())
}

// limitedBuffer captures at most limit bytes and records truncation.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.truncated = true
		b.buf.Write(p[:room])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

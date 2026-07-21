package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

// registerConsoleHandlers wires runtime.console: a bidirectional stream over
// the main process's stdin/stdout, so operators can drive interactive
// programs (game servers, REPL-style daemons) the way they would at a
// local console.
func registerConsoleHandlers(reg *rpcRegistry, registry *rt.Registry) {
	reg.stream(protocol.MethodRuntimeConsole, func(ctx context.Context, raw json.RawMessage, st *AgentStream) *protocol.Error {
		params, e := decodeParams[protocol.RuntimeConsoleParams](raw)
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
		cp, ok := instance.(rt.ConsoleProvider)
		if !ok {
			return perr(fmt.Errorf("%w: console on %s", rt.ErrNotSupported, params.Type))
		}

		// Replay recent output first so the pane is not empty on connect.
		if params.Tail > 0 {
			if streamer, ok := instance.(rt.LogStreamer); ok {
				replayBacklog(ctx, streamer, params.Tail, st)
			}
		}

		console, err := cp.Console(ctx)
		if err != nil {
			return perr(err)
		}
		defer console.Close()
		return pipeConsole(ctx, console, st)
	})
}

// replayBacklog sends the last few lines of history as ordinary output.
func replayBacklog(ctx context.Context, streamer rt.LogStreamer, tail int, st *AgentStream) {
	logs, err := streamer.Logs(ctx, rt.LogOptions{Tail: tail})
	if err != nil {
		return
	}
	defer logs.Close()
	for {
		entry, err := logs.Next(ctx)
		if err != nil {
			return
		}
		if err := st.Send(append(entry.Line, '\n')); err != nil {
			return
		}
	}
}

// pipeConsole shuttles process output to the control plane and input back,
// until either side ends.
func pipeConsole(ctx context.Context, console rt.Console, st *AgentStream) *protocol.Error {
	outErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, err := console.Read(buf)
			if n > 0 {
				if sendErr := st.Send(buf[:n]); sendErr != nil {
					outErr <- sendErr
					return
				}
			}
			if err != nil {
				outErr <- err
				return
			}
		}
	}()

	for {
		select {
		case env := <-st.In:
			if env.Op != protocol.StreamData {
				continue
			}
			if _, err := console.Write(env.Data); err != nil {
				return perr(fmt.Errorf("write to console: %w", err))
			}
		case err := <-outErr:
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return perr(err)
		case <-ctx.Done():
			return nil
		}
	}
}

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

func registerTerminalHandlers(reg *rpcRegistry, registry *rt.Registry) {
	reg.stream(protocol.MethodTerminalOpen, func(ctx context.Context, raw json.RawMessage, st *AgentStream) *protocol.Error {
		params, e := decodeParams[protocol.TerminalParams](raw)
		if e != nil {
			return e
		}
		var term rt.Terminal
		var err error
		switch params.Target {
		case protocol.TerminalTargetHost:
			term, err = openHostTerminal(params.Cols, params.Rows)
		case protocol.TerminalTargetRuntime:
			term, err = openRuntimeTerminal(ctx, registry, params)
		default:
			return &protocol.Error{Code: protocol.CodeInvalid, Message: "target must be host or runtime"}
		}
		if err != nil {
			return perr(err)
		}
		defer term.Close()
		return pipeTerminal(ctx, term, st)
	})
}

func openRuntimeTerminal(ctx context.Context, registry *rt.Registry, params protocol.TerminalParams) (rt.Terminal, error) {
	provider, err := registry.Get(rt.Type(params.Type))
	if err != nil {
		return nil, err
	}
	instance, err := provider.Get(ctx, rt.ID(params.ID))
	if err != nil {
		return nil, err
	}
	tp, ok := instance.(rt.TerminalProvider)
	if !ok {
		return nil, fmt.Errorf("%w: terminal on %s", rt.ErrNotSupported, params.Type)
	}
	return tp.Terminal(ctx, rt.TerminalSpec{Cols: params.Cols, Rows: params.Rows})
}

// pipeTerminal shuttles bytes between the PTY and the stream until either
// side ends.
func pipeTerminal(ctx context.Context, term rt.Terminal, st *AgentStream) *protocol.Error {
	outErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, err := term.Read(buf)
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
			switch env.Op {
			case protocol.StreamData:
				if _, err := term.Write(env.Data); err != nil {
					return perr(err)
				}
			case protocol.StreamCtrl:
				ctrl, err := protocol.Decode[protocol.TerminalCtrl](env.Payload)
				if err == nil && ctrl.Resize != nil {
					_ = term.Resize(ctrl.Resize.Cols, ctrl.Resize.Rows)
				}
			}
		case err := <-outErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			return perr(err)
		case <-ctx.Done():
			return nil
		}
	}
}

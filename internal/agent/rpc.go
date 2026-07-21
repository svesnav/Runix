package agent

import (
	"context"
	"encoding/json"
	"errors"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

// HandlerFunc serves one RPC method.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, *protocol.Error)

// StreamHandlerFunc serves one streaming method; it blocks until the stream
// ends and its returned error (if any) rides on the close frame.
type StreamHandlerFunc func(ctx context.Context, params json.RawMessage, st *AgentStream) *protocol.Error

type rpcRegistry struct {
	calls   map[string]HandlerFunc
	streams map[string]StreamHandlerFunc
}

func newRPCRegistry() *rpcRegistry {
	return &rpcRegistry{
		calls:   make(map[string]HandlerFunc),
		streams: make(map[string]StreamHandlerFunc),
	}
}

func (r *rpcRegistry) call(method string, h HandlerFunc)         { r.calls[method] = h }
func (r *rpcRegistry) stream(method string, h StreamHandlerFunc) { r.streams[method] = h }

// perr converts domain errors into protocol errors with stable codes.
func perr(err error) *protocol.Error {
	if err == nil {
		return nil
	}
	code := protocol.CodeInternal
	switch {
	case errors.Is(err, rt.ErrNotFound):
		code = protocol.CodeNotFound
	case errors.Is(err, rt.ErrNotSupported):
		code = protocol.CodeNotSupported
	case errors.Is(err, rt.ErrUnavailable):
		code = protocol.CodeUnavailable
	case errors.Is(err, rt.ErrInvalidSpec), errors.Is(err, rt.ErrInvalidTransition):
		code = protocol.CodeInvalid
	case errors.Is(err, context.DeadlineExceeded):
		code = protocol.CodeTimeout
	}
	return &protocol.Error{Code: code, Message: err.Error()}
}

func decodeParams[T any](raw json.RawMessage) (T, *protocol.Error) {
	v, err := protocol.Decode[T](raw)
	if err != nil {
		return v, &protocol.Error{Code: protocol.CodeInvalid, Message: err.Error()}
	}
	return v, nil
}

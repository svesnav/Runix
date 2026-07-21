package app

import (
	"context"

	"github.com/runix/runix/internal/modules/servers"
	"github.com/runix/runix/internal/protocol"
)

// serverDirectory adapts servers.Service to the hub's consumer interface.
type serverDirectory struct {
	svc *servers.Service
}

func (d serverDirectory) AuthenticateAgent(ctx context.Context, token string) (string, string, error) {
	srv, err := d.svc.Authenticate(ctx, token)
	if err != nil {
		return "", "", err
	}
	return srv.ID, srv.Name, nil
}

func (d serverDirectory) ApplyHello(ctx context.Context, serverID string, hello protocol.Hello) error {
	return d.svc.ApplyHello(ctx, serverID, hello)
}

func (d serverDirectory) MarkOnline(ctx context.Context, serverID string) {
	d.svc.MarkOnline(ctx, serverID)
}

func (d serverDirectory) MarkOffline(ctx context.Context, serverID string) {
	d.svc.MarkOffline(ctx, serverID)
}

func (d serverDirectory) RecordHeartbeat(ctx context.Context, serverID string, hb protocol.Heartbeat) error {
	return d.svc.RecordHeartbeat(ctx, serverID, hb)
}

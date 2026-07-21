package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runix/runix/internal/agent/providers/docker"
	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

// dockerClientSource is satisfied by the docker provider; declaring it here
// keeps the RPC layer from depending on the provider's concrete type beyond
// this lookup.
type dockerClientSource interface {
	Client() *docker.Client
}

// Pulling an image can take a while on slow registries.
const imagePullTimeout = 15 * time.Minute

func registerDockerResourceHandlers(reg *rpcRegistry, registry *rt.Registry) {
	client := func() (*docker.Client, *protocol.Error) {
		provider, err := registry.Get(rt.TypeDocker)
		if err != nil {
			return nil, perr(err)
		}
		source, ok := provider.(dockerClientSource)
		if !ok {
			return nil, perr(fmt.Errorf("%w: docker resource management", rt.ErrNotSupported))
		}
		return source.Client(), nil
	}

	reg.call(protocol.MethodDockerResourceList, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.DockerResourceParams](raw)
		if e != nil {
			return nil, e
		}
		c, e := client()
		if e != nil {
			return nil, e
		}
		switch params.Kind {
		case protocol.DockerImages:
			items, err := c.ListImages(ctx)
			return map[string]any{"images": items}, perr(err)
		case protocol.DockerVolumes:
			items, err := c.ListVolumes(ctx)
			return map[string]any{"volumes": items}, perr(err)
		case protocol.DockerNetworks:
			items, err := c.ListNetworks(ctx)
			return map[string]any{"networks": items}, perr(err)
		default:
			return nil, invalidKind(params.Kind)
		}
	})

	reg.call(protocol.MethodDockerResourceCreate, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.DockerResourceParams](raw)
		if e != nil {
			return nil, e
		}
		c, e := client()
		if e != nil {
			return nil, e
		}
		switch params.Kind {
		case protocol.DockerImages:
			if params.Image == "" {
				return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "image reference is required"}
			}
			pullCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imagePullTimeout)
			defer cancel()
			return nil, perr(c.PullImage(pullCtx, params.Image))
		case protocol.DockerVolumes:
			if params.Name == "" {
				return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "volume name is required"}
			}
			return nil, perr(c.CreateVolume(ctx, params.Name, params.Driver, params.Labels))
		case protocol.DockerNetworks:
			if params.Name == "" {
				return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "network name is required"}
			}
			return nil, perr(c.CreateNetwork(ctx, params.Name, params.Driver, params.Internal))
		default:
			return nil, invalidKind(params.Kind)
		}
	})

	reg.call(protocol.MethodDockerResourceRemove, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.DockerResourceParams](raw)
		if e != nil {
			return nil, e
		}
		c, e := client()
		if e != nil {
			return nil, e
		}
		if params.ID == "" {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "id is required"}
		}
		switch params.Kind {
		case protocol.DockerImages:
			return nil, perr(c.RemoveImage(ctx, params.ID, params.Force))
		case protocol.DockerVolumes:
			return nil, perr(c.RemoveVolume(ctx, params.ID, params.Force))
		case protocol.DockerNetworks:
			return nil, perr(c.RemoveNetwork(ctx, params.ID))
		default:
			return nil, invalidKind(params.Kind)
		}
	})

	reg.call(protocol.MethodDockerResourcePrune, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.DockerResourceParams](raw)
		if e != nil {
			return nil, e
		}
		c, e := client()
		if e != nil {
			return nil, e
		}
		var (
			res docker.PruneResult
			err error
		)
		switch params.Kind {
		case protocol.DockerImages:
			res, err = c.PruneImages(ctx)
		case protocol.DockerVolumes:
			res, err = c.PruneVolumes(ctx)
		case protocol.DockerNetworks:
			res, err = c.PruneNetworks(ctx)
		default:
			return nil, invalidKind(params.Kind)
		}
		if err != nil {
			return nil, perr(err)
		}
		return res, nil
	})

	reg.call(protocol.MethodDockerDiskUsage, func(ctx context.Context, _ json.RawMessage) (any, *protocol.Error) {
		c, e := client()
		if e != nil {
			return nil, e
		}
		usage, err := c.DiskUsage(ctx)
		if err != nil {
			return nil, perr(err)
		}
		return usage, nil
	})
}

func invalidKind(kind string) *protocol.Error {
	return &protocol.Error{
		Code:    protocol.CodeInvalid,
		Message: fmt.Sprintf("unknown docker resource kind %q (want images, volumes or networks)", kind),
	}
}

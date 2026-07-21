package agent

import (
	"context"
	"encoding/json"

	"github.com/runix/runix/internal/agent/providers/plugin"
	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

// registerPluginHandlers exposes what the agent loaded, so operators can see
// why a plugin is or is not active.
func registerPluginHandlers(reg *rpcRegistry, registry *rt.Registry) {
	reg.call(protocol.MethodAgentPlugins, func(ctx context.Context, _ json.RawMessage) (any, *protocol.Error) {
		out := []protocol.PluginInfo{}
		for _, provider := range registry.Providers() {
			pp, ok := provider.(*plugin.Provider)
			if !ok {
				continue
			}
			manifest := pp.Manifest()
			availability := pp.Availability(ctx)
			info := protocol.PluginInfo{
				Name:        manifest.Name,
				Version:     manifest.Version,
				RuntimeType: manifest.RuntimeType,
				Path:        manifest.Executable,
				Enabled:     availability.Available,
				Status:      "ready",
				Message:     availability.Message,
			}
			if !availability.Available {
				info.Status = "unavailable"
			}
			out = append(out, info)
		}
		return map[string]any{"plugins": out}, nil
	})
}

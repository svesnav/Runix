package agent

import (
	"context"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gopsnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"

	rt "github.com/runix/runix/internal/domain/runtime"
	"github.com/runix/runix/internal/protocol"
)

func rootPath() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

func collectHostInfo(ctx context.Context, agentVersion string) protocol.HostInfo {
	info := protocol.HostInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		AgentVersion: agentVersion,
		CPUCores:     runtime.NumCPU(),
	}
	if hi, err := host.InfoWithContext(ctx); err == nil {
		info.Hostname = hi.Hostname
		info.OSVersion = hi.Platform + " " + hi.PlatformVersion
		info.KernelVersion = hi.KernelVersion
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		info.MemoryTotal = vm.Total
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		info.SwapTotal = sw.Total
	}
	if du, err := disk.UsageWithContext(ctx, rootPath()); err == nil {
		info.DiskTotal = du.Total
	}
	return info
}

func collectHostMetrics(ctx context.Context) protocol.HostMetrics {
	var m protocol.HostMetrics
	if percents, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(percents) > 0 {
		m.CPUPercent = percents[0]
	}
	if avg, err := load.AvgWithContext(ctx); err == nil {
		m.Load1, m.Load5, m.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemoryUsed, m.MemoryTotal = vm.Used, vm.Total
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		m.SwapUsed, m.SwapTotal = sw.Used, sw.Total
	}
	if du, err := disk.UsageWithContext(ctx, rootPath()); err == nil {
		m.DiskUsed, m.DiskTotal = du.Used, du.Total
	}
	if counters, err := gopsnet.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		m.NetRxBytes, m.NetTxBytes = counters[0].BytesRecv, counters[0].BytesSent
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		m.UptimeSeconds = up
	}
	if temps, err := sensors.TemperaturesWithContext(ctx); err == nil {
		var maxTemp float64
		for _, t := range temps {
			if t.Temperature > maxTemp {
				maxTemp = t.Temperature
			}
		}
		if maxTemp > 0 {
			m.Temperature = &maxTemp
		}
	}
	return m
}

func collectProviders(ctx context.Context, registry *rt.Registry) []protocol.ProviderInfo {
	providers := registry.Providers()
	out := make([]protocol.ProviderInfo, 0, len(providers))
	for _, p := range providers {
		avail := p.Availability(ctx)
		out = append(out, protocol.ProviderInfo{
			Type:         string(p.Type()),
			Available:    avail.Available,
			Version:      avail.Version,
			Message:      avail.Message,
			Capabilities: p.Capabilities().Strings(),
		})
	}
	return out
}

func collectRuntimeCounts(ctx context.Context, registry *rt.Registry) []protocol.RuntimeCounts {
	providers := registry.Providers()
	out := make([]protocol.RuntimeCounts, 0, len(providers))
	for _, p := range providers {
		if !p.Availability(ctx).Available {
			continue
		}
		descriptors, err := p.List(ctx)
		if err != nil {
			continue
		}
		counts := protocol.RuntimeCounts{Type: string(p.Type()), States: map[string]int{}}
		for _, d := range descriptors {
			counts.States[string(d.Status.State)]++
		}
		out = append(out, counts)
	}
	return out
}

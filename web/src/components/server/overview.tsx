"use client";

import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import type { EChartsOption } from "echarts";
import { api } from "@/lib/api";
import { wsUrl } from "@/lib/ws";
import { formatBytes, formatDate, formatUptime } from "@/lib/format";
import type { MetricsPoint, Server } from "@/lib/types";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { EChart } from "@/components/echart";
import { useT } from "@/i18n";

function Fact({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wider text-ink-dim">{label}</div>
      <div className="mt-0.5 truncate text-sm">{value || "—"}</div>
    </div>
  );
}

const chartBase: EChartsOption = {
  backgroundColor: "transparent",
  grid: { left: 44, right: 12, top: 24, bottom: 24 },
  textStyle: { color: "#8b98a9", fontSize: 11 },
  xAxis: {
    type: "time",
    axisLine: { lineStyle: { color: "#232d3b" } },
    splitLine: { show: false },
  },
  tooltip: { trigger: "axis", backgroundColor: "#141b25", borderColor: "#232d3b", textStyle: { color: "#e5e9f0" } },
};

export function OverviewTab({ server }: { server: Server }) {
  const t = useT();
  const [live, setLive] = useState<MetricsPoint[]>([]);

  const { data: history } = useQuery({
    queryKey: ["metrics", server.id],
    queryFn: () => api<{ points: MetricsPoint[] }>(`/servers/${server.id}/metrics`, { query: { limit: 240 } }),
  });

  // Live heartbeats append to the chart between history refetches.
  useEffect(() => {
    let ws: WebSocket | null = null;
    try {
      ws = new WebSocket(wsUrl(`/servers/${server.id}/metrics/live`));
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type !== "metrics") return;
          const m = msg.metrics;
          setLive((prev) => [...prev.slice(-120), {
            serverId: server.id, collectedAt: msg.at,
            cpuPercent: m.cpuPercent, load1: m.load1, load5: m.load5, load15: m.load15,
            memoryUsed: m.memoryUsed, memoryTotal: m.memoryTotal,
            swapUsed: m.swapUsed, swapTotal: m.swapTotal,
            diskUsed: m.diskUsed, diskTotal: m.diskTotal,
            netRxBytes: m.netRxBytes, netTxBytes: m.netTxBytes,
            temperature: m.temperature, uptimeSecs: m.uptimeSeconds,
          }]);
        } catch { /* malformed frame */ }
      };
    } catch { /* live feed unavailable */ }
    return () => ws?.close();
  }, [server.id]);

  const points = useMemo(() => {
    const merged = new Map<string, MetricsPoint>();
    for (const p of [...(history?.points ?? [])].reverse()) merged.set(p.collectedAt, p);
    for (const p of live) merged.set(p.collectedAt, p);
    return [...merged.values()].sort(
      (a, b) => new Date(a.collectedAt).getTime() - new Date(b.collectedAt).getTime(),
    );
  }, [history, live]);

  const latest = points.at(-1);

  const cpuOption: EChartsOption = useMemo(() => ({
    ...chartBase,
    yAxis: { type: "value", max: 100, axisLabel: { formatter: "{value}%" }, splitLine: { lineStyle: { color: "#1a222e" } } },
    series: [{
      name: "CPU %", type: "line", showSymbol: false, smooth: true,
      lineStyle: { color: "#38bdf8", width: 1.5 },
      areaStyle: { color: "rgba(56,189,248,0.12)" },
      data: points.map((p) => [p.collectedAt, Math.round(p.cpuPercent * 10) / 10]),
    }],
  }), [points]);

  const memOption: EChartsOption = useMemo(() => ({
    ...chartBase,
    yAxis: { type: "value", axisLabel: { formatter: (v: number) => formatBytes(v) }, splitLine: { lineStyle: { color: "#1a222e" } } },
    series: [{
      name: "Memory", type: "line", showSymbol: false, smooth: true,
      lineStyle: { color: "#34d399", width: 1.5 },
      areaStyle: { color: "rgba(52,211,153,0.12)" },
      data: points.map((p) => [p.collectedAt, p.memoryUsed]),
    }],
  }), [points]);

  return (
    <div className="space-y-4">
      <Card>
        <CardBody className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <Fact label="Hostname" value={server.hostname} />
          <Fact label="OS" value={server.os && `${server.os} ${server.osVersion}`} />
          <Fact label="Kernel" value={server.kernelVersion} />
          <Fact label="Architecture" value={server.architecture} />
          <Fact label="CPU cores" value={server.cpuCores || "—"} />
          <Fact label="Memory" value={formatBytes(server.memoryBytes)} />
          <Fact label="Disk" value={formatBytes(server.diskBytes)} />
          <Fact label="Agent" value={server.agentVersion} />
          <Fact label="Location" value={server.location} />
          <Fact label="Uptime" value={formatUptime(latest?.uptimeSecs)} />
          <Fact label="Load" value={latest ? `${latest.load1.toFixed(2)} / ${latest.load5.toFixed(2)} / ${latest.load15.toFixed(2)}` : "—"} />
          <Fact label="Last heartbeat" value={formatDate(server.lastHeartbeatAt)} />
        </CardBody>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>{t.servers.cpuUsage}</CardTitle></CardHeader>
          <CardBody className="p-2"><EChart option={cpuOption} className="h-56 w-full" /></CardBody>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Memory {latest ? `(${formatBytes(latest.memoryUsed)} / ${formatBytes(latest.memoryTotal)})` : ""}</CardTitle>
          </CardHeader>
          <CardBody className="p-2"><EChart option={memOption} className="h-56 w-full" /></CardBody>
        </Card>
      </div>
    </div>
  );
}

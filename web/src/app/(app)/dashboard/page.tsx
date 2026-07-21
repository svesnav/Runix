"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { api } from "@/lib/api";
import { wsUrl } from "@/lib/ws";
import { timeAgo, stateBadge } from "@/lib/format";
import type { DashboardSummary } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { useT } from "@/i18n";

function Stat({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <Card>
      <CardBody>
        <div className={`text-3xl font-bold ${tone ?? ""}`}>{value}</div>
        <div className="mt-1 text-xs text-ink-dim">{label}</div>
      </CardBody>
    </Card>
  );
}

export default function DashboardPage() {
  const t = useT();
  const queryClient = useQueryClient();
  const { data } = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api<DashboardSummary>("/dashboard"),
    refetchInterval: 10_000,
  });

  // Presence events invalidate the summary immediately.
  useEffect(() => {
    let ws: WebSocket | null = null;
    try {
      ws = new WebSocket(wsUrl("/events"));
      ws.onmessage = () => queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    } catch {
      // Live updates are an enhancement; polling still covers it.
    }
    return () => ws?.close();
  }, [queryClient]);

  const servers = data?.servers ?? {};
  const runtimes = data?.runtimes ?? {};
  const events = data?.recentEvents ?? [];

  return (
    <div className="space-y-6">
      <h1 className="text-lg font-semibold">{t.dashboard.title}</h1>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Stat label={t.dashboard.servers} value={servers.total ?? 0} />
        <Stat label={t.dashboard.online} value={servers.online ?? 0} tone="text-ok" />
        <Stat label={t.dashboard.offline} value={servers.offline ?? 0} tone="text-err" />
        <Stat label={t.dashboard.connectedAgents} value={data?.connectedAgents ?? 0} tone="text-brand" />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>{t.dashboard.runtimesAcrossFleet}</CardTitle></CardHeader>
          <CardBody>
            {Object.keys(runtimes).length === 0 ? (
              <p className="text-sm text-ink-dim">{t.dashboard.noRuntimeData}</p>
            ) : (
              <div className="space-y-3">
                {Object.entries(runtimes).map(([type, states]) => (
                  <div key={type} className="flex items-center justify-between gap-2">
                    <span className="font-mono text-sm">{type}</span>
                    <div className="flex flex-wrap gap-1.5">
                      {Object.entries(states).map(([state, n]) => (
                        <Badge key={state} className={stateBadge(state)}>
                          {state}: {n}
                        </Badge>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader><CardTitle>{t.dashboard.recentEvents}</CardTitle></CardHeader>
          <CardBody>
            {events.length === 0 ? (
              <p className="text-sm text-ink-dim">{t.dashboard.nothingYet}</p>
            ) : (
              <ul className="space-y-2">
                {[...events].reverse().slice(0, 10).map((e, i) => (
                  <li key={i} className="flex items-center justify-between text-sm">
                    <span className="flex items-center gap-2">
                      <Badge className={stateBadge(e.topic === "agent.online" ? "online" : "offline")}>
                        {e.topic.replace("agent.", "")}
                      </Badge>
                      <span className="font-mono text-xs text-ink-dim">{e.serverId?.slice(0, 8)}</span>
                    </span>
                    <span className="text-xs text-ink-dim">{timeAgo(e.at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardBody>
        </Card>
      </div>
    </div>
  );
}

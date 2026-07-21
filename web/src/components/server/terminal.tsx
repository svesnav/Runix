"use client";

import { useState } from "react";
import { TerminalStatus, TerminalView } from "@/components/server/terminal-view";
import { wsUrl } from "@/lib/ws";
import { useT } from "@/i18n";

export function TerminalTab({ serverId, online }: { serverId: string; online: boolean }) {
  const t = useT();
  const [status, setStatus] = useState<TerminalStatus>("connecting");

  if (!online) {
    return <p className="py-8 text-center text-sm text-ink-dim">{t.servers.terminalOffline}</p>;
  }

  return (
    <div className="space-y-2">
      <div className="text-xs text-ink-dim">
        Host shell ·{" "}
        <span className={status === "open" ? "text-ok" : status === "closed" ? "text-err" : "text-warn"}>
          {status}
        </span>
      </div>
      <TerminalView url={wsUrl(`/servers/${serverId}/terminal`, { target: "host" })} onStatus={setStatus} />
    </div>
  );
}

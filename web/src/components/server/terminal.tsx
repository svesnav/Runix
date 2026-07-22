"use client";

import { useState } from "react";
import { wsUrl } from "@/lib/ws";
import { TerminalView, TerminalStatus } from "@/components/server/terminal-view";
import { useT } from "@/i18n";

// TerminalTab is a shell on the managed host itself.
export function TerminalTab({ serverId, online }: { serverId: string; online: boolean }) {
  const t = useT();
  const [status, setStatus] = useState<TerminalStatus>("connecting");

  if (!online) {
    return <p className="py-8 text-center text-sm text-ink-dim">{t.servers.terminalOffline}</p>;
  }

  return (
    <div className="space-y-2">
      <div className="text-xs text-ink-dim">
        {t.servers.hostShell} ·{" "}
        <span className={status === "open" ? "text-ok" : status === "closed" ? "text-err" : "text-warn"}>
          {t.runtimes.status[status]}
        </span>
        <span className="ml-2">{t.runtimes.clipboardHint}</span>
      </div>
      <TerminalView
        url={wsUrl(`/servers/${serverId}/terminal`, { target: "host" })}
        onStatus={setStatus}
      />
    </div>
  );
}

"use client";

import { useState } from "react";
import { wsUrl } from "@/lib/ws";
import { TerminalView, TerminalStatus } from "@/components/server/terminal-view";
import { useT } from "@/i18n";

// RuntimeTerminal opens a shell *inside* a runtime (a Docker container
// today) rather than on the host it runs on.
export function RuntimeTerminal({
  serverId,
  type,
  rid,
}: {
  serverId: string;
  type: string;
  rid: string;
}) {
  const t = useT();
  const [status, setStatus] = useState<TerminalStatus>("connecting");
  const [message, setMessage] = useState("");

  return (
    <div className="space-y-2">
      <div className="text-xs text-ink-dim">
        {t.runtimes.shellInside} <span className="font-mono">{rid}</span> ·{" "}
        <span className={status === "open" ? "text-ok" : status === "closed" ? "text-err" : "text-warn"}>
          {t.runtimes.status[status]}
        </span>
        {message && <span className="ml-2 text-err">{message}</span>}
        <span className="ml-2">{t.runtimes.clipboardHint}</span>
      </div>
      <TerminalView
        url={wsUrl(`/servers/${serverId}/terminal`, { target: "runtime", type, rid })}
        className="h-[50vh]"
        onStatus={setStatus}
        onEnd={(err) => err && setMessage(err)}
      />
    </div>
  );
}

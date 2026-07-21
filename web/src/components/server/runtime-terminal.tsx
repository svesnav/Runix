"use client";

import { useState } from "react";
import { TerminalStatus, TerminalView } from "@/components/server/terminal-view";
import { wsUrl } from "@/lib/ws";
import { useT } from "@/i18n";

// RuntimeTerminal opens an interactive shell inside a runtime (a Docker
// container today), as opposed to the host shell.
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
          {status}
        </span>
        {message && <span className="ml-2 text-err">{message}</span>}
        <span className="ml-2">{t.runtimes.clipboardHint}</span>
      </div>
      <TerminalView
        url={wsUrl(`/servers/${serverId}/terminal`, { target: "runtime", type, rid })}
        className="h-[50vh]"
        onStatus={setStatus}
        onEnd={(error) => setMessage(error ?? "")}
      />
    </div>
  );
}

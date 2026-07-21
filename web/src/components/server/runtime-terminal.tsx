"use client";

import { useEffect, useRef, useState } from "react";
import { b64ToBytes, textToB64, wsUrl } from "@/lib/ws";
import { attachClipboard } from "@/lib/terminal-clipboard";
import "@xterm/xterm/css/xterm.css";

// RuntimeTerminal opens an interactive shell *inside* a runtime (a Docker
// container today) as opposed to the host shell.
export function RuntimeTerminal({
  serverId,
  type,
  rid,
}: {
  serverId: string;
  type: string;
  rid: string;
}) {
  const holder = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<"connecting" | "open" | "closed">("connecting");
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (!holder.current) return;
    let disposed = false;
    let ws: WebSocket | null = null;
    let cleanup = () => {};

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !holder.current) return;

      const term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "ui-monospace, 'Cascadia Code', Consolas, monospace",
        theme: { background: "#0a0e14", foreground: "#e5e9f0", cursor: "#38bdf8" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(holder.current);
      fit.fit();

      ws = new WebSocket(
        wsUrl(`/servers/${serverId}/terminal`, {
          target: "runtime",
          type,
          rid,
          cols: term.cols,
          rows: term.rows,
        }),
      );
      ws.onopen = () => setStatus("open");
      ws.onclose = () => setStatus("closed");
      ws.onerror = () => setStatus("closed");
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type === "output" && msg.data) {
            term.write(b64ToBytes(msg.data));
          } else if (msg.type === "end") {
            if (msg.error) setMessage(msg.error);
            term.write(`\r\n\x1b[90m— session ended${msg.error ? `: ${msg.error}` : ""} —\x1b[0m\r\n`);
            setStatus("closed");
          }
        } catch {
          /* malformed frame */
        }
      };

      const dataSub = term.onData((input) => {
        if (ws?.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "input", data: textToB64(input) }));
        }
      });

      const detachClipboard = attachClipboard(term, holder.current);
      const observer = new ResizeObserver(() => {
        fit.fit();
        if (ws?.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
        }
      });
      observer.observe(holder.current);
      term.focus();

      cleanup = () => {
        detachClipboard();
        observer.disconnect();
        dataSub.dispose();
        term.dispose();
      };
    })();

    return () => {
      disposed = true;
      ws?.close();
      cleanup();
    };
  }, [serverId, type, rid]);

  return (
    <div className="space-y-2">
      <div className="text-xs text-ink-dim">
        Shell inside <span className="font-mono">{rid}</span> ·{" "}
        <span className={status === "open" ? "text-ok" : status === "closed" ? "text-err" : "text-warn"}>
          {status}
        </span>
        {message && <span className="ml-2 text-err">{message}</span>}
        <span className="ml-2">· Ctrl+Shift+C copy · Ctrl+Shift+V / right-click paste</span>
      </div>
      <div ref={holder} className="h-[50vh] overflow-hidden rounded-md border border-edge bg-canvas p-1" />
    </div>
  );
}

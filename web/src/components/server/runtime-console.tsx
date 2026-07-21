"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ClipboardCopy, CornerDownLeft, Pause, Play, Trash2 } from "lucide-react";
import { b64ToText, textToB64, wsUrl } from "@/lib/ws";
import { useT } from "@/i18n";
import { Button } from "@/components/ui/button";

interface Line {
  text: string;
  source?: string;
}

// RuntimeConsole shows a runtime's output and — when the runtime accepts
// input — lets the operator type commands straight into the process's
// stdin. That is what console-driven software (game servers, REPL daemons)
// needs: a shell opened next to the process cannot talk to it.
//
// Output is plain selectable DOM text rather than a canvas terminal, so
// select-and-copy works with the browser's own shortcuts, and the input is
// an ordinary text field, so paste works everywhere.
export function RuntimeConsole({
  serverId,
  type,
  rid,
  interactive,
}: {
  serverId: string;
  type: string;
  rid: string;
  interactive: boolean;
}) {
  const t = useT();
  const [lines, setLines] = useState<Line[]>([]);
  const [ended, setEnded] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const [input, setInput] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [historyIndex, setHistoryIndex] = useState(-1);
  const [copied, setCopied] = useState(false);

  const pane = useRef<HTMLPreElement>(null);
  const socket = useRef<WebSocket | null>(null);
  const followRef = useRef(follow);
  followRef.current = follow;
  // Partial output is buffered until a newline so a line split across
  // frames is not rendered as two lines.
  const partial = useRef("");

  const appendChunk = useCallback((chunk: string, source?: string) => {
    partial.current += chunk;
    const parts = partial.current.split("\n");
    partial.current = parts.pop() ?? "";
    if (parts.length === 0) return;
    setLines((prev) => [
      ...prev.slice(-5000),
      ...parts.map((text) => ({ text: text.replace(/\r$/, ""), source })),
    ]);
  }, []);

  useEffect(() => {
    setLines([]);
    setEnded(null);
    partial.current = "";
    let active = true;

    // The interactive console carries stdin; the read-only one is the log
    // stream. Both deliver output the same way.
    const url = interactive
      ? wsUrl(`/servers/${serverId}/runtimes/${type}/${encodeURIComponent(rid)}/console`, { tail: 200 })
      : wsUrl(`/servers/${serverId}/runtimes/${type}/${encodeURIComponent(rid)}/logs`, {
          follow: true, tail: 300,
        });

    const ws = new WebSocket(url);
    socket.current = ws;
    ws.onmessage = (ev) => {
      if (!active) return;
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === "output" && msg.data) {
          appendChunk(b64ToText(msg.data));
        } else if (msg.type === "log") {
          appendChunk(`${msg.line}\n`, msg.source);
        } else if (msg.type === "end") {
          setEnded(msg.error ?? "stream ended");
        }
      } catch {
        /* malformed frame */
      }
    };
    ws.onerror = () => {
      if (active) setEnded("connection error");
    };
    return () => {
      active = false;
      socket.current = null;
      ws.close();
    };
  }, [serverId, type, rid, interactive, appendChunk]);

  useEffect(() => {
    if (followRef.current && pane.current) {
      pane.current.scrollTo({ top: pane.current.scrollHeight });
    }
  }, [lines]);

  const send = () => {
    const ws = socket.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    // Programs read stdin line by line, so the newline is what actually
    // submits the command.
    ws.send(JSON.stringify({ type: "input", data: textToB64(input + "\n") }));
    // Echo locally: a process reading stdin does not echo it back.
    setLines((prev) => [...prev, { text: `> ${input}`, source: "input" }]);
    if (input.trim()) setHistory((prev) => [...prev.slice(-99), input]);
    setHistoryIndex(-1);
    setInput("");
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      send();
      return;
    }
    // Shell-style history with the arrow keys.
    if (e.key === "ArrowUp") {
      e.preventDefault();
      if (history.length === 0) return;
      const next = historyIndex < 0 ? history.length - 1 : Math.max(0, historyIndex - 1);
      setHistoryIndex(next);
      setInput(history[next]);
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (historyIndex < 0) return;
      const next = historyIndex + 1;
      if (next >= history.length) {
        setHistoryIndex(-1);
        setInput("");
      } else {
        setHistoryIndex(next);
        setInput(history[next]);
      }
    }
  };

  const copyAll = async () => {
    await navigator.clipboard.writeText(lines.map((l) => l.text).join("\n"));
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  const lineClass = (source?: string) => {
    switch (source) {
      case "stderr":
        return "text-err";
      case "system":
        return "text-warn";
      case "input":
        return "text-brand";
      default:
        return "text-ink";
    }
  };

  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center justify-between border-b border-edge bg-panel px-3 py-1.5">
        <span className="text-xs text-ink-dim">
          {ended ? <span className="text-warn">{ended}</span> : <span className="text-ok">{t.runtimes.live}</span>}
          <span className="ml-2">{lines.length} {t.runtimes.lines}</span>
        </span>
        <div className="flex gap-1">
          <Button size="sm" variant="ghost" onClick={copyAll} title={t.runtimes.copyOutput}>
            <ClipboardCopy size={13} className={copied ? "text-ok" : undefined} />
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setFollow((f) => !f)}
            title={follow ? t.runtimes.pauseScroll : t.runtimes.resumeScroll}>
            {follow ? <Pause size={13} /> : <Play size={13} />}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setLines([])} title={t.runtimes.clear}>
            <Trash2 size={13} />
          </Button>
        </div>
      </div>

      <pre
        ref={pane}
        onScroll={(e) => {
          const el = e.currentTarget;
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
          if (atBottom !== followRef.current) setFollow(atBottom);
        }}
        className="h-[50vh] select-text overflow-auto bg-canvas p-3 font-mono text-xs leading-5"
      >
        {lines.map((l, i) => (
          <div key={i} className={lineClass(l.source)}>{l.text}</div>
        ))}
        {lines.length === 0 && <div className="text-ink-dim">{t.runtimes.waitingOutput}</div>}
      </pre>

      {interactive && (
        <div className="flex items-center gap-2 border-t border-edge bg-panel px-3 py-2">
          <span className="font-mono text-xs text-brand">&gt;</span>
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={t.runtimes.consolePlaceholder}
            spellCheck={false}
            autoComplete="off"
            className="flex-1 bg-transparent font-mono text-xs text-ink outline-none placeholder:text-ink-dim/60"
          />
          <Button size="sm" variant="outline" onClick={send} disabled={ended !== null}>
            <CornerDownLeft size={13} /> {t.runtimes.send}
          </Button>
        </div>
      )}
      {interactive && (
        <p className="border-t border-edge bg-panel px-3 pb-2 text-[11px] text-ink-dim">
          {t.runtimes.consoleHint}
        </p>
      )}
    </div>
  );
}

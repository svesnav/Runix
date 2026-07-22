"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ClipboardCopy, ClipboardPaste, Eraser, TextSelect, ExternalLink } from "lucide-react";
import type { Terminal } from "@xterm/xterm";
import { b64ToBytes, textToB64 } from "@/lib/ws";
import { canReadClipboard, copyText, readText } from "@/lib/clipboard";
import { ContextMenu, MenuItem } from "@/components/ui/context-menu";
import { useT } from "@/i18n";
import "@xterm/xterm/css/xterm.css";

export type TerminalStatus = "connecting" | "open" | "closed";

// TerminalView owns everything a Runix terminal does other than deciding
// which endpoint to dial: the xterm instance, the socket bridge, resizing,
// and the clipboard. The host shell and the container shell differ only in
// their URL, so they share this.
export function TerminalView({
  url,
  className = "h-[65vh]",
  onStatus,
  onEnd,
}: {
  url: string;
  className?: string;
  onStatus?: (s: TerminalStatus) => void;
  onEnd?: (error?: string) => void;
}) {
  const t = useT();
  const holder = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  // The selection is captured when the menu opens: reading it during a
  // later render would consult a ref React never re-rendered for, so Copy
  // could appear disabled with text plainly selected.
  const [menu, setMenu] = useState<{ x: number; y: number; selection: string } | null>(null);
  const [copied, setCopied] = useState(false);

  // Kept in refs so the effect below never has to re-run (and re-dial) when
  // a parent re-renders with new callback identities.
  const statusCb = useRef(onStatus);
  statusCb.current = onStatus;
  const endCb = useRef(onEnd);
  endCb.current = onEnd;

  useEffect(() => {
    if (!holder.current) return;
    let disposed = false;
    let ws: WebSocket | null = null;
    let cleanup = () => {};

    (async () => {
      const [{ Terminal: XTerm }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !holder.current) return;

      const instance = new XTerm({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "ui-monospace, 'Cascadia Code', Consolas, monospace",
        theme: { background: "#0a0e14", foreground: "#e5e9f0", cursor: "#38bdf8" },
        // Right-click drives our own menu, so xterm must not also treat it
        // as a selection gesture.
        rightClickSelectsWord: false,
      });
      const fit = new FitAddon();
      instance.loadAddon(fit);
      instance.open(holder.current);
      fit.fit();
      term.current = instance;

      const socket = new WebSocket(`${url}${url.includes("?") ? "&" : "?"}cols=${instance.cols}&rows=${instance.rows}`);
      ws = socket;
      socket.onopen = () => statusCb.current?.("open");
      socket.onclose = () => statusCb.current?.("closed");
      socket.onerror = () => statusCb.current?.("closed");
      socket.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type === "output" && msg.data) {
            instance.write(b64ToBytes(msg.data));
          } else if (msg.type === "end") {
            instance.write(`\r\n\x1b[90m— ${t.runtimes.sessionEnded}${msg.error ? `: ${msg.error}` : ""} —\x1b[0m\r\n`);
            statusCb.current?.("closed");
            endCb.current?.(msg.error);
          }
        } catch {
          /* malformed frame */
        }
      };

      const dataSub = instance.onData((input) => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "input", data: textToB64(input) }));
        }
      });

      // Ctrl+Shift+C/V, because plain Ctrl+C and Ctrl+V are control codes
      // the running program needs (SIGINT and ^V). Returning false stops
      // xterm from also forwarding the keystroke.
      instance.attachCustomKeyEventHandler((e) => {
        if (e.type !== "keydown" || !e.ctrlKey || !e.shiftKey) return true;
        const key = e.key.toLowerCase();
        if (key === "c") {
          const sel = instance.getSelection();
          if (!sel) return true; // nothing selected — let the process see it
          void copyText(sel);
          return false;
        }
        if (key === "v") {
          void readText().then((text) => {
            if (text) instance.paste(text);
          });
          return false;
        }
        return true;
      });

      const observer = new ResizeObserver(() => {
        fit.fit();
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "resize", cols: instance.cols, rows: instance.rows }));
        }
      });
      observer.observe(holder.current);
      instance.focus();

      cleanup = () => {
        observer.disconnect();
        dataSub.dispose();
        instance.dispose();
        term.current = null;
      };
    })();

    return () => {
      disposed = true;
      ws?.close();
      cleanup();
    };
    // t is read inside, but re-dialling the socket on a language switch
    // would be worse than a stale "session ended" string.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url]);

  // Bound natively in the capture phase rather than with onContextMenu:
  // xterm handles the event on its own elements, and React's delegated
  // listener at the document root never sees it.
  useEffect(() => {
    const el = holder.current;
    if (!el) return;
    const onMenu = (e: MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      setMenu({ x: e.clientX, y: e.clientY, selection: term.current?.getSelection() ?? "" });
    };
    el.addEventListener("contextmenu", onMenu, true);
    return () => el.removeEventListener("contextmenu", onMenu, true);
  }, []);

  const doCopy = useCallback(async (text: string) => {
    if (!text) return;
    const success = await copyText(text);
    if (success) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }, []);

  const doPaste = useCallback(async () => {
    const text = await readText();
    if (text) {
      term.current?.paste(text);
    } else {
      // Fallback: user can press Ctrl+V which triggers the browser's paste event
      // The terminal will receive it as a paste event
      term.current?.focus();
    }
  }, []);

  const doOpenInBrowser = useCallback(() => {
    const selection = menu?.selection ?? "";
    if (!selection) return;
    
    // Try to detect and open URLs in the selection
    const urlRegex = /https?:\/\/[^\s/$.?#].[^\s]*/gi;
    const urls = selection.match(urlRegex);
    
    if (urls && urls.length > 0) {
      // Open the first URL found in a new tab
      const url = urls[0];
      window.open(url, "_blank", "noopener,noreferrer");
    }
  }, [menu?.selection]);

  const readable = canReadClipboard();

  const items: MenuItem[] = [
    {
      label: t.runtimes.copy,
      icon: <ClipboardCopy size={13} />,
      disabled: !menu?.selection,
      onSelect: () => void doCopy(menu?.selection ?? ""),
    },
    {
      // Without the Clipboard API there is no way to read what the operator
      // copied, so the item says to use the browser's own paste instead of
      // appearing broken.
      label: readable ? t.runtimes.paste : t.runtimes.pasteWithCtrlV,
      icon: <ClipboardPaste size={13} />,
      disabled: !readable,
      onSelect: () => void doPaste(),
    },
    {
      label: t.runtimes.openInBrowser,
      icon: <ExternalLink size={13} />,
      disabled: !menu?.selection,
      onSelect: () => void doOpenInBrowser(),
      separatorBefore: true,
    },
    {
      label: t.runtimes.selectAll,
      icon: <TextSelect size={13} />,
      onSelect: () => { term.current?.selectAll(); term.current?.focus(); },
    },
    {
      label: t.runtimes.clear,
      icon: <Eraser size={13} />,
      onSelect: () => { term.current?.clear(); term.current?.focus(); },
    },
  ];

  return (
    <>
      <div
        ref={holder}
        className={`${className} overflow-hidden rounded-md border border-edge bg-canvas p-1`}
      />
      {copied && (
        <span className="pointer-events-none fixed bottom-6 left-1/2 z-50 -translate-x-1/2 rounded-md border border-edge bg-panel px-3 py-1.5 text-xs text-ok shadow-lg">
          {t.runtimes.copiedToClipboard}
        </span>
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={items} onClose={() => setMenu(null)} />}
    </>
  );
}

"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";

// controlPlaneURL is where agents dial in: the configured API origin, or the
// current origin when the UI is served same-origin as the API.
function controlPlaneURL(): string {
  const configured = process.env.NEXT_PUBLIC_API_URL;
  if (configured) return configured;
  if (typeof window !== "undefined") return window.location.origin;
  return "https://runix.example.com";
}

// AgentInstall renders the complete, copy-paste command that enrolls an
// agent: control-plane URL plus the one-time token.
export function AgentInstall({ token }: { token: string }) {
  const [copied, setCopied] = useState(false);
  const command =
    `RUNIX_AGENT_SERVER_URL=${controlPlaneURL()} \\\n` +
    `RUNIX_AGENT_TOKEN=${token} \\\n` +
    `./runix-agent`;

  const copy = async () => {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs text-ink-dim">Run this on the target server:</span>
        <Button size="sm" variant="outline" onClick={copy}>
          {copied ? <Check size={13} className="text-ok" /> : <Copy size={13} />}
          {copied ? "Copied" : "Copy command"}
        </Button>
      </div>
      <pre className="overflow-x-auto rounded-md border border-edge bg-canvas p-3 font-mono text-xs leading-5 text-ink">
        {command}
      </pre>
      <p className="text-[11px] text-ink-dim">
        The agent dials out to the control plane over WebSocket — no inbound ports need opening on the server.
      </p>
    </div>
  );
}

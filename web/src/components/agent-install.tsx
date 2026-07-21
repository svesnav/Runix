"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";

// controlPlaneURL is where agents dial in: the configured API origin, or the
// current origin when the UI is served same-origin as the API.
function controlPlaneURL(): string {
  const configured = process.env.NEXT_PUBLIC_API_URL;
  if (configured) return configured;
  if (typeof window !== "undefined") return window.location.origin;
  return "https://runix.example.com";
}

// The installer is published as a release asset, so a new host needs
// nothing but curl — no binary to fetch or unpack first.
const INSTALLER_URL =
  process.env.NEXT_PUBLIC_INSTALLER_URL ??
  "https://github.com/svesnav/Runix/releases/latest/download/install.sh";

// AgentInstall renders the complete, copy-paste command that installs and
// enrolls an agent: the installer, the control-plane URL, and the
// one-time token.
export function AgentInstall({ token }: { token: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const command =
    `curl -fsSL ${INSTALLER_URL} | sudo sh -s -- \\\n` +
    `  --role agent \\\n` +
    `  --url ${controlPlaneURL()} \\\n` +
    `  --token ${token}`;

  const copy = async () => {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs text-ink-dim">{t.servers.runOnServer}</span>
        <Button size="sm" variant="outline" onClick={copy}>
          {copied ? <Check size={13} className="text-ok" /> : <Copy size={13} />}
          {copied ? t.servers.copied : t.servers.copyCommand}
        </Button>
      </div>
      <pre className="overflow-x-auto rounded-md border border-edge bg-canvas p-3 font-mono text-xs leading-5 text-ink">
        {command}
      </pre>
      <p className="text-[11px] text-ink-dim">
        {t.servers.dialsOut}
      </p>
    </div>
  );
}

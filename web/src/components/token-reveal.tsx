"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";

// TokenReveal shows a one-time secret with a copy button.
export function TokenReveal({ token }: { token: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(token);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="flex items-center gap-2">
      <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-edge bg-canvas px-3 py-2 font-mono text-xs">
        {token}
      </code>
      <Button variant="outline" size="sm" onClick={copy}>
        {copied ? <Check size={13} className="text-ok" /> : <Copy size={13} />}
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}

"use client";

import dynamic from "next/dynamic";
import { Button } from "@/components/ui/button";

const MonacoEditor = dynamic(() => import("@monaco-editor/react"), { ssr: false });

export const SAMPLE_COMPOSE = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    restart: unless-stopped
`;

// ComposeEditor is the shared YAML surface for creating and editing compose
// projects.
export function ComposeEditor({
  value,
  onChange,
  path,
}: {
  value: string;
  onChange: (v: string) => void;
  path?: string;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <MonacoEditor
        height="45vh"
        theme="vs-dark"
        language="yaml"
        path={path ?? "compose.yaml"}
        value={value}
        onChange={(v) => onChange(v ?? "")}
        options={{ fontSize: 12, minimap: { enabled: false }, scrollBeyondLastLine: false, tabSize: 2 }}
      />
    </div>
  );
}

export function ComposeHint({ dir }: { dir?: string }) {
  return (
    <p className="text-xs text-ink-dim">
      {dir
        ? <>Written to <span className="font-mono text-ink">{dir}</span> and applied with <span className="font-mono">docker compose up -d</span>.</>
        : <>Saved into the agent&apos;s compose directory and applied with <span className="font-mono">docker compose up -d</span>.</>}
    </p>
  );
}

export function DialogFooter({
  onCancel,
  onSubmit,
  submitLabel,
  disabled,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  submitLabel: string;
  disabled?: boolean;
}) {
  return (
    <div className="flex justify-end gap-2">
      <Button type="button" variant="outline" onClick={onCancel}>Cancel</Button>
      <Button onClick={onSubmit} disabled={disabled}>{submitLabel}</Button>
    </div>
  );
}

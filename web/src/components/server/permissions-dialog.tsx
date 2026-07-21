"use client";

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { FSEntry } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";

const CLASSES = [
  { key: "owner", label: "Owner" },
  { key: "group", label: "Group" },
  { key: "other", label: "Others" },
] as const;

const BITS = [
  { key: "read", label: "Read", value: 4 },
  { key: "write", label: "Write", value: 2 },
  { key: "exec", label: "Execute", value: 1 },
] as const;

// modeFromString parses the rwx portion of a Go FileMode string
// ("-rw-r--r--", "drwxr-xr-x") into its octal digits.
function digitsFromModeString(mode: string): [number, number, number] {
  const rwx = mode.slice(-9);
  const digit = (offset: number) => {
    let v = 0;
    if (rwx[offset] === "r") v += 4;
    if (rwx[offset + 1] === "w") v += 2;
    if (rwx[offset + 2] === "x" || rwx[offset + 2] === "s" || rwx[offset + 2] === "t") v += 1;
    return v;
  };
  return [digit(0), digit(3), digit(6)];
}

export function PermissionsDialog({
  serverId,
  entry,
  onClose,
  onDone,
}: {
  serverId: string;
  entry: FSEntry;
  onClose: () => void;
  onDone: () => void;
}) {
  const [digits, setDigits] = useState<[number, number, number]>(() => digitsFromModeString(entry.mode));
  const [recursive, setRecursive] = useState(false);
  const [error, setError] = useState("");

  const octal = digits.join("");

  const save = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/files/chmod`, {
        method: "POST",
        body: { path: entry.path, mode: octal, recursive },
      }),
    onSuccess: () => {
      onDone();
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "chmod failed"),
  });

  const toggle = (classIndex: number, bit: number) => {
    const next: [number, number, number] = [...digits] as [number, number, number];
    next[classIndex] = next[classIndex] ^ bit;
    setDigits(next);
  };

  return (
    <Dialog open onClose={onClose} title={`Permissions — ${entry.name}`}>
      <div className="space-y-4">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-[11px] uppercase tracking-wider text-ink-dim">
              <th className="pb-1 text-left">Class</th>
              {BITS.map((b) => (
                <th key={b.key} className="pb-1 text-center">{b.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {CLASSES.map((cls, i) => (
              <tr key={cls.key}>
                <td className="py-1">{cls.label}</td>
                {BITS.map((bit) => (
                  <td key={bit.key} className="py-1 text-center">
                    <input
                      type="checkbox"
                      className="accent-brand"
                      checked={(digits[i] & bit.value) !== 0}
                      onChange={() => toggle(i, bit.value)}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>

        <div className="grid grid-cols-2 items-end gap-4">
          <Field label="Octal mode">
            <Input
              className="font-mono"
              value={octal}
              onChange={(e) => {
                const v = e.target.value.replace(/[^0-7]/g, "").slice(0, 3);
                if (v.length === 3) {
                  setDigits([Number(v[0]), Number(v[1]), Number(v[2])]);
                }
              }}
            />
          </Field>
          <p className="pb-2 font-mono text-xs text-ink-dim">current: {entry.mode}</p>
        </div>

        {entry.isDir && (
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="accent-brand"
              checked={recursive}
              onChange={(e) => setRecursive(e.target.checked)}
            />
            Apply recursively to all contents
          </label>
        )}

        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={() => save.mutate()} disabled={save.isPending}>Apply</Button>
        </div>
      </div>
    </Dialog>
  );
}

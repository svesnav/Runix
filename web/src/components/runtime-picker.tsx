"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { RuntimeInfo } from "@/lib/types";
import { Field, Input, Select } from "@/components/ui/input";
import { useT } from "@/i18n";

export interface RuntimeRef {
  type: string;
  id: string;
}

const TYPES = ["daemon", "docker", "compose", "systemd"];

// RuntimePicker lists what is actually on a server instead of asking the
// operator to remember an identifier. Runtimes can only be listed while
// the agent is connected, so callers choose what happens when it is not:
//
//   allowManual  — offer type + id fields, for places where a wrong value
//                  simply fails later and is visible (a scheduled task).
//   without it   — no fallback, for authorization, where a typo would
//                  create a grant that silently matches nothing.
export function RuntimePicker({
  serverId,
  value,
  onChange,
  allowManual = false,
  label,
}: {
  serverId: string;
  value: RuntimeRef;
  onChange: (next: RuntimeRef) => void;
  allowManual?: boolean;
  label?: string;
}) {
  const t = useT();

  const { data, error, isLoading } = useQuery({
    queryKey: ["runtimes", serverId, "all"],
    queryFn: () => api<{ runtimes: RuntimeInfo[] }>(`/servers/${serverId}/runtimes`),
    enabled: serverId !== "",
    retry: false,
  });

  const runtimes = data?.runtimes ?? [];
  const unreachable = Boolean(error);

  // Group by type so a host with 130 systemd units stays navigable.
  const byType = TYPES.map((type) => ({
    type,
    items: runtimes.filter((r) => r.descriptor.type === type),
  })).filter((g) => g.items.length > 0);

  const selected = value.type && value.id ? `${value.type}/${value.id}` : "";
  const known = runtimes.some(
    (r) => r.descriptor.type === value.type && r.descriptor.id === value.id,
  );

  if (unreachable && allowManual) {
    return (
      <div className="grid grid-cols-2 gap-4">
        <Field label={t.schedule.runtimeType}>
          <Select value={value.type} onChange={(e) => onChange({ ...value, type: e.target.value })}>
            {TYPES.map((ty) => <option key={ty} value={ty}>{ty}</option>)}
          </Select>
        </Field>
        <Field label={t.schedule.runtimeId}>
          <Input
            className="font-mono"
            value={value.id}
            onChange={(e) => onChange({ ...value, id: e.target.value })}
            placeholder="my-service"
          />
          <p className="mt-1 text-[11px] text-warn">{t.runtimes.pickerOffline}</p>
        </Field>
      </div>
    );
  }

  return (
    <Field label={label ?? t.runtimes.runtime}>
      <Select
        value={selected}
        disabled={!serverId || isLoading}
        onChange={(e) => {
          const [type, ...rest] = e.target.value.split("/");
          onChange({ type, id: rest.join("/") });
        }}
      >
        <option value="">
          {!serverId ? t.runtimes.pickerChooseServer : isLoading ? t.common.loading : t.grants.select}
        </option>
        {/* A saved task can point at something no longer present — keep it
            selectable rather than silently switching to another runtime. */}
        {selected && !known && !isLoading && (
          <option value={selected}>{value.type} · {value.id}</option>
        )}
        {byType.map((group) => (
          <optgroup key={group.type} label={group.type}>
            {group.items.map((rt) => (
              <option
                key={`${rt.descriptor.type}/${rt.descriptor.id}`}
                value={`${rt.descriptor.type}/${rt.descriptor.id}`}
              >
                {rt.descriptor.name}
              </option>
            ))}
          </optgroup>
        ))}
      </Select>
      {unreachable && (
        <p className="mt-1 text-[11px] text-warn">{t.grants.runtimesUnavailable}</p>
      )}
    </Field>
  );
}

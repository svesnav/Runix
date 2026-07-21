"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { formatDate } from "@/lib/format";
import type { Setting, SettingDescriptor } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useT } from "@/i18n";
import { BackupSection } from "@/components/backup-section";

interface SettingsResponse {
  settings: Setting[];
  keys: SettingDescriptor[];
}

export default function SettingsPage() {
  const t = useT();
  const queryClient = useQueryClient();
  const [drafts, setDrafts] = useState<Record<string, unknown>>({});
  const [error, setError] = useState("");

  const { data } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsResponse>("/settings"),
  });

  const save = useMutation({
    mutationFn: (input: { key: string; value: unknown }) =>
      api(`/settings/${input.key}`, { method: "PUT", body: { value: input.value } }),
    onSuccess: (_, input) => {
      setError("");
      setDrafts((d) => { const next = { ...d }; delete next[input.key]; return next; });
      queryClient.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : t.settings.saveFailed),
  });

  const stored = new Map((data?.settings ?? []).map((s) => [s.key, s]));
  const descriptors = data?.keys ?? [];

  // Group order follows the order the control plane declares them in.
  const groups: { name: string; items: SettingDescriptor[] }[] = [];
  for (const d of descriptors) {
    const g = groups.find((x) => x.name === d.group);
    if (g) g.items.push(d);
    else groups.push({ name: d.group, items: [d] });
  }

  // The wording lives in the UI so it can be translated; the control plane's
  // own label is the fallback for a key this build has never heard of.
  const label = (d: SettingDescriptor) => t.settings.keys[d.key]?.label ?? d.label;
  const describe = (d: SettingDescriptor) => t.settings.keys[d.key]?.description ?? d.description;
  const groupName = (name: string) => t.settings.groups[name] ?? name;

  const valueOf = (d: SettingDescriptor): unknown => {
    if (d.key in drafts) return drafts[d.key];
    const s = stored.get(d.key);
    if (s !== undefined) return s.value;
    return d.default ?? (d.kind === "bool" ? false : d.kind === "int" ? 0 : "");
  };

  const setDraft = (key: string, value: unknown) =>
    setDrafts((d) => ({ ...d, [key]: value }));

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">{t.settings.title}</h1>
      {error && <p className="text-xs text-err">{error}</p>}

      {groups.map((group) => (
        <Card key={group.name}>
          <CardHeader><CardTitle>{groupName(group.name)}</CardTitle></CardHeader>
          <CardBody className="divide-y divide-edge p-0">
            {group.items.map((d) => {
              const value = valueOf(d);
              const dirty = d.key in drafts;
              const setting = stored.get(d.key);
              return (
                <div key={d.key} className="flex flex-wrap items-center gap-4 px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm text-ink">{label(d)}</p>
                    <p className="mt-0.5 text-xs text-ink-dim">{describe(d)}</p>
                    <p className="mt-1 text-[11px] text-ink-dim/70">
                      {setting
                        ? `${t.settings.changed} ${formatDate(setting.updatedAt)}`
                        : t.settings.usingDefault}
                    </p>
                  </div>

                  <div className="flex items-center gap-2">
                    {d.kind === "bool" ? (
                      <label className="flex cursor-pointer items-center gap-2 text-xs text-ink-dim">
                        <input
                          type="checkbox"
                          className="size-4 accent-brand"
                          checked={Boolean(value)}
                          onChange={(e) => setDraft(d.key, e.target.checked)}
                        />
                        {value ? t.common.enabled : t.common.disabled}
                      </label>
                    ) : d.kind === "int" ? (
                      <>
                        <Input
                          type="number"
                          className="h-8 w-28"
                          min={d.min}
                          max={d.max}
                          value={String(value)}
                          onChange={(e) => setDraft(d.key, Number(e.target.value))}
                        />
                        {d.unit && (
                          <span className="text-xs text-ink-dim">{t.settings.units[d.unit] ?? d.unit}</span>
                        )}
                      </>
                    ) : (
                      <Input
                        className="h-8 w-56"
                        value={String(value)}
                        onChange={(e) => setDraft(d.key, e.target.value)}
                      />
                    )}

                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!dirty || save.isPending}
                      onClick={() => save.mutate({ key: d.key, value })}
                    >
                      {t.common.save}
                    </Button>
                  </div>
                </div>
              );
            })}
          </CardBody>
        </Card>
      ))}

      <BackupSection />
    </div>
  );
}

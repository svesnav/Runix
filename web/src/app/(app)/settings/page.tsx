"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { formatDate } from "@/lib/format";
import type { Setting } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { useT } from "@/i18n";
import { BackupSection } from "@/components/backup-section";

interface SettingsResponse {
  settings: Setting[];
  knownKeys: string[];
}

export default function SettingsPage() {
  const t = useT();
  const queryClient = useQueryClient();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [error, setError] = useState("");

  const { data } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsResponse>("/settings"),
  });

  const save = useMutation({
    mutationFn: (input: { key: string; raw: string }) => {
      let value: unknown;
      try {
        value = JSON.parse(input.raw);
      } catch {
        throw new ApiError(400, "invalid", `value for ${input.key} must be valid JSON (e.g. 7, true, "name")`);
      }
      return api(`/settings/${input.key}`, { method: "PUT", body: { value } });
    },
    onSuccess: (_, input) => {
      setError("");
      setDrafts((d) => { const next = { ...d }; delete next[input.key]; return next; });
      queryClient.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  const current = new Map((data?.settings ?? []).map((s) => [s.key, s]));

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">{t.settings.title}</h1>
      {error && <p className="text-xs text-err">{error}</p>}
      <Card>
        <Table>
          <THead>
            <TR><TH>Key</TH><TH>Value (JSON)</TH><TH>Updated</TH><TH className="text-right">Save</TH></TR>
          </THead>
          <TBody>
            {(data?.knownKeys ?? []).sort().map((key) => {
              const setting = current.get(key);
              const value = drafts[key] ?? (setting ? JSON.stringify(setting.value) : "");
              return (
                <TR key={key}>
                  <TD className="font-mono text-xs">{key}</TD>
                  <TD>
                    <Input
                      className="h-8 max-w-xs font-mono text-xs"
                      value={value}
                      placeholder="unset"
                      onChange={(e) => setDrafts((d) => ({ ...d, [key]: e.target.value }))}
                    />
                  </TD>
                  <TD className="text-xs text-ink-dim">{setting ? formatDate(setting.updatedAt) : "default"}</TD>
                  <TD className="text-right">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={drafts[key] === undefined || save.isPending}
                      onClick={() => save.mutate({ key, raw: drafts[key] })}
                    >
                      Save
                    </Button>
                  </TD>
                </TR>
              );
            })}
          </TBody>
        </Table>
      </Card>

      <BackupSection />
    </div>
  );
}

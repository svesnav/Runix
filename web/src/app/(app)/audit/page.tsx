"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "@/lib/api";
import { formatDate, stateBadge } from "@/lib/format";
import type { AuditEntry, ListResponse } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { useT } from "@/i18n";

export default function AuditPage() {
  const t = useT();
  const [page, setPage] = useState(1);
  const [action, setAction] = useState("");

  const { data } = useQuery({
    queryKey: ["audit", page, action],
    queryFn: () =>
      api<ListResponse<AuditEntry>>("/audit", {
        query: { page, size: 50, action: action || undefined },
      }),
  });

  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.size)) : 1;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-lg font-semibold">{t.audit.title}</h1>
        <Input
          className="max-w-56"
          placeholder={t.audit.filterPlaceholder}
          value={action}
          onChange={(e) => { setAction(e.target.value); setPage(1); }}
        />
      </div>

      <Card>
        <Table>
          <THead>
            <TR>
              <TH>{t.audit.time}</TH><TH>{t.audit.actor}</TH><TH>{t.audit.action}</TH>
              <TH>{t.audit.target}</TH><TH>{t.audit.result}</TH><TH>{t.audit.ip}</TH>
            </TR>
          </THead>
          <TBody>
            {(data?.items ?? []).map((entry) => (
              <TR key={entry.id}>
                <TD className="whitespace-nowrap text-xs text-ink-dim">{formatDate(entry.time)}</TD>
                <TD className="text-xs">{entry.actorName || "—"}</TD>
                <TD className="font-mono text-xs">{entry.action}</TD>
                <TD className="max-w-64 truncate font-mono text-xs text-ink-dim" title={`${entry.targetType}:${entry.targetId}`}>
                  {entry.targetId || "—"}
                </TD>
                <TD>
                  <Badge className={stateBadge(entry.result)} >{entry.result}</Badge>
                  {entry.error && <span className="ml-2 text-xs text-err">{entry.error}</span>}
                </TD>
                <TD className="font-mono text-xs text-ink-dim">{entry.ip || "—"}</TD>
              </TR>
            ))}
            {data && data.items.length === 0 && (
              <TR><TD colSpan={6} className="py-8 text-center text-ink-dim">{t.audit.empty}</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      <div className="flex items-center justify-end gap-3 text-sm">
        <span className="text-xs text-ink-dim">
          {t.common.page} {data?.page ?? page} {t.common.of} {totalPages} · {data?.total ?? 0} {t.common.entries}
        </span>
        <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          {t.common.previous}
        </Button>
        <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
          {t.common.next}
        </Button>
      </div>
    </div>
  );
}

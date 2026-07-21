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

export default function AuditPage() {
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
        <h1 className="text-lg font-semibold">Audit log</h1>
        <Input
          className="max-w-56"
          placeholder="Filter by action, e.g. auth.login"
          value={action}
          onChange={(e) => { setAction(e.target.value); setPage(1); }}
        />
      </div>

      <Card>
        <Table>
          <THead>
            <TR><TH>Time</TH><TH>Actor</TH><TH>Action</TH><TH>Target</TH><TH>Result</TH><TH>IP</TH></TR>
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
              <TR><TD colSpan={6} className="py-8 text-center text-ink-dim">No entries</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      <div className="flex items-center justify-end gap-3 text-sm">
        <span className="text-xs text-ink-dim">
          Page {data?.page ?? page} of {totalPages} · {data?.total ?? 0} entries
        </span>
        <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
          Previous
        </Button>
        <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
          Next
        </Button>
      </div>
    </div>
  );
}

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatDate } from "@/lib/format";
import type {
  Grant, Group, ListResponse, PermissionDescriptor, Role, Server, ServerGroup, User,
} from "@/lib/types";
import { useT } from "@/i18n";
import { GrantDialog } from "@/components/grant-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";

export default function GrantsPage() {
  const t = useT();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);

  const { data: grants } = useQuery({
    queryKey: ["grants"],
    queryFn: () => api<{ grants: Grant[] }>("/grants"),
  });
  const { data: users } = useQuery({
    queryKey: ["users"],
    queryFn: () => api<ListResponse<User>>("/users", { query: { size: 200 } }),
  });
  const { data: groups } = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: Group[] }>("/groups"),
  });
  const { data: servers } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<ListResponse<Server>>("/servers", { query: { size: 200 } }),
  });
  const { data: roles } = useQuery({
    queryKey: ["roles"],
    queryFn: () => api<{ roles: Role[] }>("/roles"),
  });
  const { data: serverGroups } = useQuery({
    queryKey: ["server-groups"],
    queryFn: () => api<{ groups: ServerGroup[] }>("/server-groups"),
  });
  const { data: perms } = useQuery({
    queryKey: ["permissions"],
    queryFn: () => api<{ permissions: PermissionDescriptor[] }>("/permissions"),
  });

  // Grants store opaque subject/scope ids; resolve them to names so the
  // table never shows a raw uuid.
  const names = useMemo(() => {
    const map = new Map<string, string>();
    for (const u of users?.items ?? []) map.set(u.id, u.displayName || u.username);
    for (const g of groups?.groups ?? []) map.set(g.id, g.name);
    for (const r of roles?.roles ?? []) map.set(r.id, r.name || r.key);
    for (const s of servers?.items ?? []) map.set(s.id, s.name);
    for (const g of serverGroups?.groups ?? []) map.set(g.id, g.name);
    return map;
  }, [users, groups, roles, servers, serverGroups]);

  const permissionLabel = (key: string) =>
    perms?.permissions.find((p) => p.key === key)?.label ?? key;

  // A runtime scope id is "<serverId>/<type>/<runtimeId>"; render it with
  // the server's name instead of its uuid.
  const scopeLabel = (grant: Grant): string => {
    if (grant.scopeType === "global") return t.grants.scopeGlobal;
    if (grant.scopeType === "runtime") {
      const [srv, type, ...rest] = (grant.scopeId ?? "").split("/");
      return `${names.get(srv) ?? srv} · ${type}/${rest.join("/")}`;
    }
    return names.get(grant.scopeId ?? "") ?? grant.scopeId ?? "—";
  };

  const revoke = useMutation({
    mutationFn: (id: string) => api(`/grants/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["grants"] }),
    onError: (err) => alert(err instanceof ApiError ? err.message : "revoke failed"),
  });

  const label = (id: string) => names.get(id) ?? `${id.slice(0, 8)}…`;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">{t.grants.title}</h1>
          <p className="mt-1 max-w-3xl text-xs text-ink-dim">{t.grants.intro}</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus size={15} /> {t.grants.newGrant}</Button>
      </div>

      <Card>
        <Table>
          <THead>
            <TR>
              <TH>{t.grants.subject}</TH><TH>{t.grants.permission}</TH><TH>{t.grants.scope}</TH>
              <TH>{t.common.created}</TH><TH className="text-right" />
            </TR>
          </THead>
          <TBody>
            {(grants?.grants ?? []).map((g) => (
              <TR key={g.id}>
                <TD>
                  <Badge className="mr-2 border-edge bg-card text-ink-dim">{g.subjectType}</Badge>
                  <span className="text-sm">{label(g.subjectId)}</span>
                </TD>
                <TD className="text-xs">{permissionLabel(g.permission)}</TD>
                <TD className="text-xs">
                  {g.scopeType === "global"
                    ? <span className="text-ink-dim">{t.grants.scopeGlobal}</span>
                    : <>
                        <span className="text-ink-dim">{g.scopeType.replace("_", " ")}:</span>{" "}
                        {scopeLabel(g)}
                      </>}
                </TD>
                <TD className="text-xs text-ink-dim">{formatDate(g.createdAt)}</TD>
                <TD className="text-right">
                  <Button size="sm" variant="ghost"
                    onClick={() => { if (confirm(t.grants.revokeConfirm)) revoke.mutate(g.id); }}>
                    <Trash2 size={13} className="text-err" />
                  </Button>
                </TD>
              </TR>
            ))}
            {grants && grants.grants.length === 0 && (
              <TR><TD colSpan={5} className="py-8 text-center text-ink-dim">{t.grants.empty}</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      {createOpen && <GrantDialog onClose={() => setCreateOpen(false)} />}
    </div>
  );
}

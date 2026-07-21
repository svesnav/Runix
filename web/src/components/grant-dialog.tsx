"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import type {
  Grant, Group, ListResponse, PermissionDescriptor, Role, RuntimeInfo, Server, ServerGroup, User,
} from "@/lib/types";
import { useT } from "@/i18n";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Select } from "@/components/ui/input";

// runtimeScopeId must match the server's rbac.RuntimeScopeID: the server is
// part of the id so a grant never leaks to another host's runtime of the
// same name.
export function runtimeScopeId(serverId: string, type: string, id: string): string {
  return `${serverId}/${type}/${id}`;
}

export interface GrantPreset {
  scopeType?: Grant["scopeType"];
  serverId?: string;
  runtimeType?: string;
  runtimeId?: string;
  permission?: string;
}

// GrantDialog creates a grant with every field chosen from a list — no
// hand-typed identifiers anywhere.
export function GrantDialog({
  preset,
  onClose,
  onDone,
}: {
  preset?: GrantPreset;
  onClose: () => void;
  onDone?: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();

  const [subjectType, setSubjectType] = useState<Grant["subjectType"]>("user");
  const [subjectId, setSubjectId] = useState("");
  const [permission, setPermission] = useState(preset?.permission ?? "");
  const [scopeType, setScopeType] = useState<Grant["scopeType"]>(preset?.scopeType ?? "global");
  const [serverId, setServerId] = useState(preset?.serverId ?? "");
  const [scopeId, setScopeId] = useState("");
  const [runtimeRef, setRuntimeRef] = useState(
    preset?.runtimeType && preset?.runtimeId ? `${preset.runtimeType}/${preset.runtimeId}` : "",
  );
  const [error, setError] = useState("");

  const { data: users } = useQuery({
    queryKey: ["users"],
    queryFn: () => api<ListResponse<User>>("/users", { query: { size: 200 } }),
  });
  const { data: groups } = useQuery({
    queryKey: ["groups"],
    queryFn: () => api<{ groups: Group[] }>("/groups"),
  });
  const { data: roles } = useQuery({
    queryKey: ["roles"],
    queryFn: () => api<{ roles: Role[] }>("/roles"),
  });
  const { data: servers } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<ListResponse<Server>>("/servers", { query: { size: 200 } }),
  });
  const { data: serverGroups } = useQuery({
    queryKey: ["server-groups"],
    queryFn: () => api<{ groups: ServerGroup[] }>("/server-groups"),
  });
  const { data: perms } = useQuery({
    queryKey: ["permissions"],
    queryFn: () => api<{ permissions: PermissionDescriptor[] }>("/permissions"),
  });

  // Runtimes are only listable once a server is chosen, and only while its
  // agent is connected.
  const { data: runtimes, error: runtimesError } = useQuery({
    queryKey: ["runtimes", serverId, "all"],
    queryFn: () => api<{ runtimes: RuntimeInfo[] }>(`/servers/${serverId}/runtimes`),
    enabled: scopeType === "runtime" && serverId !== "",
    retry: false,
  });

  // Reset the dependent selection whenever the scope changes.
  useEffect(() => {
    setScopeId("");
    if (scopeType !== "runtime") setRuntimeRef("");
  }, [scopeType]);

  const subjects =
    subjectType === "user"
      ? (users?.items ?? []).map((u) => ({ id: u.id, label: u.displayName || u.username }))
      : subjectType === "group"
        ? (groups?.groups ?? []).map((g) => ({ id: g.id, label: g.name }))
        : (roles?.roles ?? []).map((r) => ({ id: r.id, label: r.name || r.key }));

  const effectiveScopeId = (): string => {
    switch (scopeType) {
      case "global":
        return "";
      case "server":
        return serverId;
      case "server_group":
        return scopeId;
      case "runtime": {
        const [type, ...rest] = runtimeRef.split("/");
        return runtimeScopeId(serverId, type, rest.join("/"));
      }
    }
  };

  const create = useMutation({
    mutationFn: () =>
      api("/grants", {
        method: "POST",
        body: { subjectType, subjectId, permission, scopeType, scopeId: effectiveScopeId() },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["grants"] });
      onDone?.();
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  const scopeReady =
    scopeType === "global" ||
    (scopeType === "server" && serverId) ||
    (scopeType === "server_group" && scopeId) ||
    (scopeType === "runtime" && serverId && runtimeRef);

  return (
    <Dialog open onClose={onClose} title={t.grants.newGrant}>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.grants.subjectType}>
            <Select
              value={subjectType}
              onChange={(e) => {
                setSubjectType(e.target.value as Grant["subjectType"]);
                setSubjectId("");
              }}
            >
              <option value="user">{t.grants.user}</option>
              <option value="group">{t.grants.userGroup}</option>
              <option value="role">{t.grants.role}</option>
            </Select>
          </Field>
          <Field
            label={
              subjectType === "user" ? t.grants.user
                : subjectType === "group" ? t.grants.userGroup
                  : t.grants.role
            }
          >
            <Select value={subjectId} onChange={(e) => setSubjectId(e.target.value)}>
              <option value="">{t.grants.select}</option>
              {subjects.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
            </Select>
            {subjects.length === 0 && (
              <p className="mt-1 text-[11px] text-warn">
                {subjectType === "group" ? t.grants.noGroupsHint : t.grants.noneAvailable}
              </p>
            )}
          </Field>
        </div>

        <Field label={t.grants.permission}>
          <Select value={permission} onChange={(e) => setPermission(e.target.value)}>
            <option value="">{t.grants.select}</option>
            {(perms?.permissions ?? []).map((p) => (
              <option key={p.key} value={p.key}>{p.label}</option>
            ))}
          </Select>
        </Field>

        <Field label={t.grants.scope}>
          <Select
            value={scopeType}
            onChange={(e) => setScopeType(e.target.value as Grant["scopeType"])}
          >
            <option value="global">{t.grants.scopeGlobal}</option>
            <option value="server">{t.grants.scopeServer}</option>
            <option value="server_group">{t.grants.scopeServerGroup}</option>
            <option value="runtime">{t.grants.scopeRuntime}</option>
          </Select>
        </Field>

        {(scopeType === "server" || scopeType === "runtime") && (
          <Field label={t.grants.server}>
            <Select value={serverId} onChange={(e) => { setServerId(e.target.value); setRuntimeRef(""); }}>
              <option value="">{t.grants.select}</option>
              {(servers?.items ?? []).map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </Select>
          </Field>
        )}

        {scopeType === "server_group" && (
          <Field label={t.grants.scopeServerGroup}>
            <Select value={scopeId} onChange={(e) => setScopeId(e.target.value)}>
              <option value="">{t.grants.select}</option>
              {(serverGroups?.groups ?? []).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </Select>
            {(serverGroups?.groups ?? []).length === 0 && (
              <p className="mt-1 text-[11px] text-warn">{t.grants.noServerGroupsHint}</p>
            )}
          </Field>
        )}

        {scopeType === "runtime" && serverId && (
          <Field label={t.grants.runtime}>
            <Select value={runtimeRef} onChange={(e) => setRuntimeRef(e.target.value)}>
              <option value="">{t.grants.select}</option>
              {(runtimes?.runtimes ?? []).map((rt) => (
                <option
                  key={`${rt.descriptor.type}/${rt.descriptor.id}`}
                  value={`${rt.descriptor.type}/${rt.descriptor.id}`}
                >
                  {rt.descriptor.type} · {rt.descriptor.name}
                </option>
              ))}
            </Select>
            {runtimesError && (
              <p className="mt-1 text-[11px] text-warn">{t.grants.runtimesUnavailable}</p>
            )}
          </Field>
        )}

        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!subjectId || !permission || !scopeReady || create.isPending}>
            {t.grants.grant}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

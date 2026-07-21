"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Trash2, UserPlus, X } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import type { Group, ListResponse, Server, ServerGroup, User } from "@/lib/types";
import { useT } from "@/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";

// GroupManager handles both group flavours: user groups (members are users)
// and server groups (members are servers). They share the same shape, so one
// component covers both rather than duplicating the CRUD.
type Flavour = "users" | "servers";

const config = {
  users: {
    listPath: "/groups",
    memberIdsKey: "userIds",
    memberParam: "userId",
  },
  servers: {
    listPath: "/server-groups",
    memberIdsKey: "serverIds",
    memberParam: "serverId",
  },
} as const;

export function GroupManager({ flavour }: { flavour: Flavour }) {
  const t = useT();
  const queryClient = useQueryClient();
  const cfg = config[flavour];
  const [createOpen, setCreateOpen] = useState(false);
  const [membersFor, setMembersFor] = useState<Group | ServerGroup | null>(null);

  const { data } = useQuery({
    queryKey: [cfg.listPath],
    queryFn: () => api<{ groups: (Group | ServerGroup)[] }>(cfg.listPath),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: [cfg.listPath] });

  const remove = useMutation({
    mutationFn: (id: string) => api(`${cfg.listPath}/${id}`, { method: "DELETE" }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "delete failed"),
  });

  const groups = data?.groups ?? [];
  const title = flavour === "users" ? t.groups.userGroups : t.groups.serverGroups;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
          <Plus size={13} /> {t.groups.newGroup}
        </Button>
      </CardHeader>
      <CardBody className="space-y-2">
        <p className="text-xs text-ink-dim">
          {flavour === "users" ? t.groups.userGroupsHint : t.groups.serverGroupsHint}
        </p>
        {groups.length === 0 ? (
          <p className="py-4 text-center text-sm text-ink-dim">{t.groups.empty}</p>
        ) : (
          <div className="space-y-1.5">
            {groups.map((group) => (
              <div key={group.id} className="flex items-center justify-between rounded-md border border-edge px-3 py-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium">{group.name}</div>
                  {group.description && (
                    <div className="truncate text-[11px] text-ink-dim">{group.description}</div>
                  )}
                </div>
                <div className="flex gap-1">
                  <Button size="sm" variant="ghost" onClick={() => setMembersFor(group)}>
                    <UserPlus size={13} /> {t.groups.members}
                  </Button>
                  <Button size="sm" variant="ghost"
                    onClick={() => { if (confirm(t.groups.deleteConfirm)) remove.mutate(group.id); }}>
                    <Trash2 size={13} className="text-err" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </CardBody>

      {createOpen && (
        <CreateGroupDialog flavour={flavour} onClose={() => setCreateOpen(false)} onDone={invalidate} />
      )}
      {membersFor && (
        <MembersDialog flavour={flavour} group={membersFor} onClose={() => setMembersFor(null)} />
      )}
    </Card>
  );
}

function CreateGroupDialog({
  flavour, onClose, onDone,
}: {
  flavour: Flavour;
  onClose: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const cfg = config[flavour];
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => api(cfg.listPath, { method: "POST", body: { name, description } }),
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  return (
    <Dialog open onClose={onClose} title={t.groups.newGroup}>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
        <Field label={t.common.name}>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)}
            placeholder={flavour === "users" ? "backend-team" : "production" } />
        </Field>
        <Field label={t.common.description}>
          <Input value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!name.trim() || create.isPending}>{t.common.create}</Button>
        </div>
      </form>
    </Dialog>
  );
}

function MembersDialog({
  flavour, group, onClose,
}: {
  flavour: Flavour;
  group: Group | ServerGroup;
  onClose: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const cfg = config[flavour];
  const [toAdd, setToAdd] = useState("");
  const [error, setError] = useState("");

  const membersKey = ["group-members", flavour, group.id];
  const { data: members } = useQuery({
    queryKey: membersKey,
    queryFn: () => api<Record<string, string[]>>(`${cfg.listPath}/${group.id}/members`),
  });

  // Candidates come from the matching directory.
  const { data: users } = useQuery({
    queryKey: ["users"],
    queryFn: () => api<ListResponse<User>>("/users", { query: { size: 200 } }),
    enabled: flavour === "users",
  });
  const { data: servers } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<ListResponse<Server>>("/servers", { query: { size: 200 } }),
    enabled: flavour === "servers",
  });

  const memberIds = members?.[cfg.memberIdsKey] ?? [];
  const candidates =
    flavour === "users"
      ? (users?.items ?? []).map((u) => ({ id: u.id, label: u.displayName || u.username }))
      : (servers?.items ?? []).map((s) => ({ id: s.id, label: s.name }));

  const invalidate = () => queryClient.invalidateQueries({ queryKey: membersKey });

  const add = useMutation({
    mutationFn: (id: string) =>
      api(`${cfg.listPath}/${group.id}/members`, {
        method: "POST",
        body: { [cfg.memberParam]: id },
      }),
    onSuccess: () => { setToAdd(""); invalidate(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "add failed"),
  });

  const removeMember = useMutation({
    mutationFn: (id: string) =>
      api(`${cfg.listPath}/${group.id}/members/${id}`, { method: "DELETE" }),
    onSuccess: invalidate,
    onError: (err) => setError(err instanceof ApiError ? err.message : "remove failed"),
  });

  const labelFor = (id: string) => candidates.find((c) => c.id === id)?.label ?? id;
  const available = candidates.filter((c) => !memberIds.includes(c.id));

  return (
    <Dialog open onClose={onClose} title={`${t.groups.members} — ${group.name}`}>
      <div className="space-y-4">
        <div className="flex flex-wrap gap-1.5">
          {memberIds.map((id) => (
            <Badge key={id} className="border-edge bg-card text-ink">
              {labelFor(id)}
              <button
                onClick={() => removeMember.mutate(id)}
                className="ml-1.5 cursor-pointer text-ink-dim hover:text-err"
                aria-label={t.common.remove}
              >
                <X size={11} />
              </button>
            </Badge>
          ))}
          {memberIds.length === 0 && <span className="text-sm text-ink-dim">{t.groups.noMembers}</span>}
        </div>

        <div className="flex items-end gap-2">
          <div className="flex-1">
            <Field label={t.groups.addMember}>
              <Select value={toAdd} onChange={(e) => setToAdd(e.target.value)}>
                <option value="">{t.grants.select}</option>
                {available.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
              </Select>
            </Field>
          </div>
          <Button onClick={() => toAdd && add.mutate(toAdd)} disabled={!toAdd || add.isPending}>
            <Plus size={13} /> {t.groups.add}
          </Button>
        </div>

        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end">
          <Button variant="outline" onClick={onClose}>{t.common.close}</Button>
        </div>
      </div>
    </Dialog>
  );
}

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { KeyRound, Plus, Shield, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatDate, stateBadge } from "@/lib/format";
import type { ListResponse, Role, User } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { GroupManager } from "@/components/group-manager";

export default function UsersPage() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [rolesFor, setRolesFor] = useState<User | null>(null);
  const [passwordFor, setPasswordFor] = useState<User | null>(null);
  const [error, setError] = useState("");

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["users"] });

  const { data } = useQuery({
    queryKey: ["users"],
    queryFn: () => api<ListResponse<User>>("/users", { query: { size: 200 } }),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(`/users/${id}`, { method: "DELETE" }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "delete failed"),
  });

  const toggleActive = useMutation({
    mutationFn: (u: User) =>
      api(`/users/${u.id}`, {
        method: "PUT",
        body: { email: u.email, displayName: u.displayName, isActive: !u.isActive },
      }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "update failed"),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Users</h1>
        <Button onClick={() => { setError(""); setCreateOpen(true); }}>
          <Plus size={15} /> Add user
        </Button>
      </div>

      <Card>
        <Table>
          <THead>
            <TR><TH>Username</TH><TH>Email</TH><TH>Status</TH><TH>MFA</TH><TH>Created</TH><TH className="text-right">Actions</TH></TR>
          </THead>
          <TBody>
            {(data?.items ?? []).map((u) => (
              <TR key={u.id}>
                <TD className="font-medium">{u.displayName ? `${u.displayName} (${u.username})` : u.username}</TD>
                <TD className="text-xs">{u.email}</TD>
                <TD>
                  <button className="cursor-pointer" onClick={() => toggleActive.mutate(u)} title="Toggle active">
                    <Badge className={stateBadge(u.isActive ? "online" : "offline")}>
                      {u.isActive ? "active" : "disabled"}
                    </Badge>
                  </button>
                </TD>
                <TD>
                  <Badge className={u.totpEnabled ? stateBadge("online") : "border-edge bg-card text-ink-dim"}>
                    {u.totpEnabled ? "TOTP" : "none"}
                  </Badge>
                </TD>
                <TD className="text-xs text-ink-dim">{formatDate(u.createdAt)}</TD>
                <TD>
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" title="Roles" onClick={() => setRolesFor(u)}>
                      <Shield size={13} />
                    </Button>
                    <Button size="sm" variant="ghost" title="Reset password" onClick={() => setPasswordFor(u)}>
                      <KeyRound size={13} />
                    </Button>
                    <Button size="sm" variant="ghost" title="Delete"
                      onClick={() => { if (confirm(`Delete user ${u.username}?`)) remove.mutate(u.id); }}>
                      <Trash2 size={13} className="text-err" />
                    </Button>
                  </div>
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </Card>

      <GroupManager flavour="users" />

      <CreateUserDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onDone={invalidate}
        error={error}
        setError={setError}
      />
      {rolesFor && <RolesDialog user={rolesFor} onClose={() => setRolesFor(null)} />}
      {passwordFor && <ResetPasswordDialog user={passwordFor} onClose={() => setPasswordFor(null)} />}
    </div>
  );
}

function CreateUserDialog({
  open, onClose, onDone, error, setError,
}: {
  open: boolean; onClose: () => void; onDone: () => void;
  error: string; setError: (s: string) => void;
}) {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api("/users", {
        method: "POST",
        body: { username, email, displayName, password, mustChangePassword: true },
      }),
    onSuccess: () => {
      onDone(); onClose();
      setUsername(""); setEmail(""); setDisplayName(""); setPassword("");
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  return (
    <Dialog open={open} onClose={onClose} title="Add user">
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label="Username"><Input value={username} onChange={(e) => setUsername(e.target.value)} /></Field>
          <Field label="Display name"><Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} /></Field>
        </div>
        <Field label="Email"><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></Field>
        <Field label="Initial password (change forced at first login)">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={!username || !email || !password || create.isPending}>Create</Button>
        </div>
      </form>
    </Dialog>
  );
}

function RolesDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const { data: allRoles } = useQuery({
    queryKey: ["roles"],
    queryFn: () => api<{ roles: Role[] }>("/roles"),
  });
  const { data: userRoles } = useQuery({
    queryKey: ["user-roles", user.id],
    queryFn: () => api<{ roles: string[] }>(`/users/${user.id}/roles`),
  });
  const [selected, setSelected] = useState<Set<string> | null>(null);
  const [error, setError] = useState("");

  const current = selected ?? new Set(
    (allRoles?.roles ?? []).filter((r) => (userRoles?.roles ?? []).includes(r.key)).map((r) => r.id),
  );

  const save = useMutation({
    mutationFn: () =>
      api(`/users/${user.id}/roles`, { method: "PUT", body: { roleIds: [...current] } }),
    onSuccess: onClose,
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  return (
    <Dialog open onClose={onClose} title={`Roles — ${user.username}`}>
      <div className="space-y-2">
        {(allRoles?.roles ?? []).map((role) => (
          <label key={role.id} className="flex cursor-pointer items-center gap-2 rounded-md border border-edge px-3 py-2 text-sm hover:bg-card/60">
            <input
              type="checkbox"
              className="accent-brand"
              checked={current.has(role.id)}
              onChange={(e) => {
                const next = new Set(current);
                if (e.target.checked) next.add(role.id);
                else next.delete(role.id);
                setSelected(next);
              }}
            />
            <span className="font-medium">{role.key}</span>
            <span className="text-xs text-ink-dim">{role.permissions.length} permissions</span>
          </label>
        ))}
      </div>
      {error && <p className="mt-3 text-xs text-err">{error}</p>}
      <div className="mt-4 flex justify-end gap-2">
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button onClick={() => save.mutate()} disabled={save.isPending}>Save</Button>
      </div>
    </Dialog>
  );
}

function ResetPasswordDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const reset = useMutation({
    mutationFn: () =>
      api(`/users/${user.id}/password`, { method: "PUT", body: { newPassword: password } }),
    onSuccess: onClose,
    onError: (err) => setError(err instanceof ApiError ? err.message : "reset failed"),
  });
  return (
    <Dialog open onClose={onClose} title={`Reset password — ${user.username}`}>
      <form onSubmit={(e) => { e.preventDefault(); reset.mutate(); }} className="space-y-4">
        <Field label="New password (change forced at next login)">
          <Input autoFocus type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={!password || reset.isPending}>Reset</Button>
        </div>
      </form>
    </Dialog>
  );
}

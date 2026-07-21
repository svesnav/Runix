"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import type { PermissionDescriptor, Role } from "@/lib/types";
import { useT } from "@/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";

// labelFor resolves a stored permission key to its readable name.
function labelFor(key: string, catalog: PermissionDescriptor[] | undefined): string {
  return catalog?.find((p) => p.key === key)?.label ?? key;
}

export default function RolesPage() {
  const t = useT();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<Role | "new" | null>(null);

  const { data: roles } = useQuery({
    queryKey: ["roles"],
    queryFn: () => api<{ roles: Role[] }>("/roles"),
  });
  const { data: perms } = useQuery({
    queryKey: ["permissions"],
    queryFn: () => api<{ permissions: PermissionDescriptor[] }>("/permissions"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(`/roles/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["roles"] }),
    onError: (err) => alert(err instanceof ApiError ? err.message : "delete failed"),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">{t.roles.title}</h1>
        <Button onClick={() => setEditing("new")}>
          <Plus size={15} /> {t.roles.newRole}
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {(roles?.roles ?? []).map((role) => (
          <Card key={role.id}>
            <CardHeader>
              <div className="flex items-center gap-2">
                <CardTitle>{role.key}</CardTitle>
                {role.isSystem && <Badge className="border-edge bg-card text-ink-dim">system</Badge>}
              </div>
              <div className="flex gap-1">
                {!role.isSystem && (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => setEditing(role)}>Edit</Button>
                    <Button size="sm" variant="ghost"
                      onClick={() => { if (confirm(`Delete role ${role.key}?`)) remove.mutate(role.id); }}>
                      <Trash2 size={13} className="text-err" />
                    </Button>
                  </>
                )}
              </div>
            </CardHeader>
            <CardBody>
              <div className="flex flex-wrap gap-1">
                {role.permissions.map((p) => (
                  <Badge key={p} className="border-edge bg-card text-ink-dim" >
                    {labelFor(p, perms?.permissions)}
                  </Badge>
                ))}
                {role.permissions.length === 0 && (
                  <span className="text-xs text-ink-dim">{t.roles.noPermissions}</span>
                )}
              </div>
            </CardBody>
          </Card>
        ))}
      </div>

      {editing && (
        <RoleDialog
          role={editing === "new" ? null : editing}
          allPermissions={perms?.permissions ?? []}
          onClose={() => setEditing(null)}
          onDone={() => queryClient.invalidateQueries({ queryKey: ["roles"] })}
        />
      )}
    </div>
  );
}

// groupLabel turns a catalog group id into a heading.
function groupLabel(group: string): string {
  const labels: Record<string, string> = {
    servers: "Servers",
    files: "Files",
    runtime: "Runtimes",
    access: "Users & access",
    system: "System",
  };
  return labels[group] ?? group;
}

function RoleDialog({
  role, allPermissions, onClose, onDone,
}: {
  role: Role | null;
  allPermissions: PermissionDescriptor[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [key, setKey] = useState(role?.key ?? "");
  const [name, setName] = useState(role?.name ?? "");
  const [description, setDescription] = useState(role?.description ?? "");
  const [selected, setSelected] = useState<Set<string>>(new Set(role?.permissions ?? []));
  const [error, setError] = useState("");

  // Group by the catalog's own grouping rather than by key prefix.
  const groups = new Map<string, PermissionDescriptor[]>();
  for (const p of allPermissions) {
    groups.set(p.group, [...(groups.get(p.group) ?? []), p]);
  }

  const save = useMutation({
    mutationFn: () => {
      const body = { key, name: name || key, description, permissions: [...selected] };
      return role
        ? api(`/roles/${role.id}`, { method: "PUT", body })
        : api("/roles", { method: "POST", body });
    },
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  return (
    <Dialog open onClose={onClose} title={role ? `Edit role — ${role.key}` : "New role"} wide>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-3 gap-4">
          <Field label="Key">
            <Input value={key} onChange={(e) => setKey(e.target.value)} disabled={!!role} placeholder="deployer" />
          </Field>
          <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label="Description"><Input value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
        </div>
        <div className="grid max-h-[45vh] grid-cols-1 gap-x-6 gap-y-4 overflow-y-auto md:grid-cols-2">
          {[...groups.entries()].map(([group, permsInGroup]) => (
            <div key={group}>
              <div className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-ink-dim">
                {groupLabel(group)}
              </div>
              {permsInGroup.map((p) => (
                <label
                  key={p.key}
                  className="flex cursor-pointer items-start gap-2 py-1 text-sm"
                  title={p.description}
                >
                  <input
                    type="checkbox"
                    className="mt-0.5 accent-brand"
                    checked={selected.has(p.key)}
                    onChange={(e) => {
                      const next = new Set(selected);
                      if (e.target.checked) next.add(p.key);
                      else next.delete(p.key);
                      setSelected(next);
                    }}
                  />
                  <span>
                    {p.label}
                    <span className="block text-[11px] text-ink-dim">{p.description}</span>
                  </span>
                </label>
              ))}
            </div>
          ))}
        </div>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={!key || save.isPending}>Save</Button>
        </div>
      </form>
    </Dialog>
  );
}

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { KeyRound, Pencil, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { stateBadge } from "@/lib/format";
import { serverPath } from "@/lib/routes";
import type { Server } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Tabs } from "@/components/ui/tabs";
import { AgentInstall } from "@/components/agent-install";
import { OverviewTab } from "@/components/server/overview";
import { RuntimesTab } from "@/components/server/runtimes";
import { DockerResourcesTab } from "@/components/server/docker-resources";
import { FilesTab } from "@/components/server/files";
import { TerminalTab } from "@/components/server/terminal";
import { useT } from "@/i18n";

export default function ServerDetailPage() {
  const t = useT();
  return (
    <Suspense fallback={<p className="text-sm text-ink-dim">{t.common.loading}</p>}>
      <ServerDetail />
    </Suspense>
  );
}

function ServerDetail() {
  const t = useT();
  const router = useRouter();
  const params = useSearchParams();
  const queryClient = useQueryClient();

  const id = params.get("id") ?? "";
  const tab = params.get("tab") ?? "overview";
  const filePath = params.get("path") ?? "/";
  const setTab = (next: string) => router.replace(serverPath(id, { tab: next }));

  const [rotated, setRotated] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [editOpen, setEditOpen] = useState(false);

  const { data: server } = useQuery({
    queryKey: ["server", id],
    queryFn: () => api<Server>(`/servers/${id}`),
    refetchInterval: 15_000,
  });

  const rotate = useMutation({
    mutationFn: () => api<{ agentToken: string }>(`/servers/${id}/token/rotate`, { method: "POST" }),
    onSuccess: (res) => setRotated(res.agentToken),
  });

  const remove = useMutation({
    mutationFn: () => api(`/servers/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["servers"] });
      router.replace("/servers");
    },
  });

  if (!server) return <p className="text-sm text-ink-dim">{t.common.loading}</p>;

  const online = server.connectionStatus === "online";

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-semibold">{server.name}</h1>
            <Badge className={stateBadge(server.connectionStatus)}>
              {server.connectionStatus.replace("_", " ")}
            </Badge>
          </div>
          <p className="mt-1 text-xs text-ink-dim">
            {[server.address, server.description || server.hostname].filter(Boolean).join(" · ") || server.id}
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => setEditOpen(true)}>
            <Pencil size={13} /> {t.common.edit}
          </Button>
          <Button variant="outline" size="sm" onClick={() => rotate.mutate()}>
            <KeyRound size={13} /> {t.servers.rotateToken}
          </Button>
          <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>
            <Trash2 size={13} /> {t.common.delete}
          </Button>
        </div>
      </div>

      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { id: "overview", label: t.servers.tabs.overview },
          { id: "runtimes", label: t.servers.tabs.runtimes },
          ...(server.dockerAvailable ? [{ id: "docker", label: t.servers.tabs.docker }] : []),
          { id: "files", label: t.servers.tabs.files },
          { id: "terminal", label: t.servers.tabs.terminal },
        ]}
      />

      {tab === "overview" && <OverviewTab server={server} />}
      {tab === "runtimes" && (
        <RuntimesTab serverId={server.id} online={online} availableTypes={server.runtimeTypes} />
      )}
      {tab === "docker" && <DockerResourcesTab serverId={server.id} online={online} />}
      {tab === "files" && <FilesTab serverId={server.id} online={online} initialPath={filePath} />}
      {tab === "terminal" && <TerminalTab serverId={server.id} online={online} />}

      <Dialog open={rotated !== null} onClose={() => setRotated(null)} title={t.servers.newTokenTitle}>
        {rotated && (
          <div className="space-y-3">
            <p className="text-sm text-ink-dim">{t.servers.newTokenIntro}</p>
            <AgentInstall token={rotated} />
          </div>
        )}
      </Dialog>

      {editOpen && (
        <EditServerDialog server={server} onClose={() => setEditOpen(false)} onDone={() => {
          queryClient.invalidateQueries({ queryKey: ["server", id] });
          queryClient.invalidateQueries({ queryKey: ["servers"] });
        }} />
      )}

      <Dialog open={confirmDelete} onClose={() => setConfirmDelete(false)} title={t.servers.deleteTitle}>
        <p className="text-sm text-ink-dim">
          {t.common.delete} <b className="text-ink">{server.name}</b> {t.servers.deleteConfirm}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={() => setConfirmDelete(false)}>{t.common.cancel}</Button>
          <Button variant="danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
            {t.servers.deleteServer}
          </Button>
        </div>
      </Dialog>
    </div>
  );
}

function EditServerDialog({
  server, onClose, onDone,
}: {
  server: Server;
  onClose: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(server.name);
  const [address, setAddress] = useState(server.address);
  const [description, setDescription] = useState(server.description);
  const [location, setLocation] = useState(server.location);
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: () =>
      api(`/servers/${server.id}`, {
        method: "PUT",
        body: { name, address, description, location, tags: server.tags, labels: server.labels },
      }),
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  return (
    <Dialog open onClose={onClose} title={t.servers.editTitle}>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.common.name}><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
        </div>
        <Field label={t.servers.location}><Input value={location} onChange={(e) => setLocation(e.target.value)} /></Field>
        <Field label={t.common.description}><Textarea rows={2} value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!name || save.isPending}>{t.common.save}</Button>
        </div>
      </form>
    </Dialog>
  );
}

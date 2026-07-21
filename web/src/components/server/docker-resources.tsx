"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Download, Eraser, Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatBytes, formatDate } from "@/lib/format";
import type { DockerDiskUsage, DockerImage, DockerNetwork, DockerVolume } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { useT } from "@/i18n";

// Labels are resolved inside the component: the dictionary comes from a
// hook, which module scope cannot call.
const KINDS = [
  { id: "images", labelKey: "images" as const },
  { id: "volumes", labelKey: "volumes" as const },
  { id: "networks", labelKey: "networks" as const },
];

export function DockerResourcesTab({ serverId, online }: { serverId: string; online: boolean }) {
  const t = useT();
  const [kind, setKind] = useState("images");
  const tabs = KINDS.map((k) => ({ id: k.id, label: t.docker[k.labelKey] }));

  if (!online) {
    return <p className="py-8 text-center text-sm text-ink-dim">{t.docker.agentOffline}</p>;
  }

  return (
    <div className="space-y-4">
      <DiskUsage serverId={serverId} />
      <Tabs items={tabs} value={kind} onChange={setKind} />
      {kind === "images" && <Images serverId={serverId} />}
      {kind === "volumes" && <Volumes serverId={serverId} />}
      {kind === "networks" && <Networks serverId={serverId} />}
    </div>
  );
}

function DiskUsage({ serverId }: { serverId: string }) {
  const { data } = useQuery({
    queryKey: ["docker-usage", serverId],
    queryFn: () => api<DockerDiskUsage>(`/servers/${serverId}/docker/usage`),
    retry: false,
  });
  if (!data) return null;
  return (
    <div className="flex flex-wrap gap-4 text-xs text-ink-dim">
      <span>Images <span className="text-ink">{formatBytes(data.imagesSize)}</span></span>
      <span>Containers <span className="text-ink">{formatBytes(data.containersSize)}</span></span>
      <span>Volumes <span className="text-ink">{formatBytes(data.volumesSize)}</span></span>
    </div>
  );
}

// useResource centralizes the list/create/remove/prune calls shared by all
// three resource kinds.
function useResource<T>(serverId: string, kind: string, listKey: string) {
  const t = useT();
  const queryClient = useQueryClient();
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["docker", serverId, kind] });
    queryClient.invalidateQueries({ queryKey: ["docker-usage", serverId] });
  };

  const query = useQuery({
    queryKey: ["docker", serverId, kind],
    queryFn: () => api<Record<string, T[]>>(`/servers/${serverId}/docker/${kind}`),
    refetchInterval: 20_000,
  });

  const remove = useMutation({
    mutationFn: (input: { id: string; force?: boolean }) =>
      api(`/servers/${serverId}/docker/${kind}/${encodeURIComponent(input.id)}`, {
        method: "DELETE",
        query: { force: input.force ?? false },
      }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "remove failed"),
  });

  const prune = useMutation({
    mutationFn: () => api<{ reclaimedBytes?: number; deleted?: number }>(
      `/servers/${serverId}/docker/${kind}/prune`, { method: "POST" }),
    onSuccess: (res) => {
      invalidate();
      const freed = res?.reclaimedBytes ? ` (${formatBytes(res.reclaimedBytes)} reclaimed)` : "";
      alert(`Pruned ${res?.deleted ?? 0} unused ${kind}${freed}`);
    },
    onError: (err) => alert(err instanceof ApiError ? err.message : "prune failed"),
  });

  return { items: (query.data?.[listKey] ?? []) as T[], error: query.error, remove, prune, invalidate };
}

function Images({ serverId }: { serverId: string }) {
  const t = useT();
  const { items, error, remove, prune, invalidate } = useResource<DockerImage>(serverId, "images", "images");
  const [pullOpen, setPullOpen] = useState(false);

  return (
    <div className="space-y-3">
      <Toolbar
        count={items.length}
        label="image"
        onPrune={() => { if (confirm(t.docker.pruneImagesConfirm)) prune.mutate(); }}
        pruning={prune.isPending}
        action={<Button size="sm" onClick={() => setPullOpen(true)}><Download size={13} /> Pull image</Button>}
      />
      <ErrorLine error={error} />
      <Card>
        <Table>
          <THead>
            <TR><TH>{t.docker.tags}</TH><TH>{t.common.size}</TH><TH>{t.docker.inUse}</TH><TH>{t.common.created}</TH><TH className="text-right">{t.common.actions}</TH></TR>
          </THead>
          <TBody>
            {items.map((img) => (
              <TR key={img.id}>
                <TD className="max-w-96 font-mono text-xs">
                  {img.repoTags.length ? img.repoTags.join(", ") : <span className="text-ink-dim">&lt;none&gt;</span>}
                  {img.dangling && <Badge className="ml-2 border-warn/30 bg-warn/10 text-warn">dangling</Badge>}
                </TD>
                <TD className="text-xs">{formatBytes(img.size)}</TD>
                <TD className="text-xs">{img.containers > 0 ? `${img.containers} container(s)` : "—"}</TD>
                <TD className="text-xs text-ink-dim">{formatDate(new Date(img.created * 1000).toISOString())}</TD>
                <TD className="text-right">
                  <Button size="sm" variant="ghost" title={t.common.remove}
                    onClick={() => {
                      const name = img.repoTags[0] ?? img.id;
                      if (confirm(t.docker.confirmRemoveImage.replace("{name}", name))) remove.mutate({ id: img.repoTags[0] ?? img.id, force: true });
                    }}>
                    <Trash2 size={13} className="text-err" />
                  </Button>
                </TD>
              </TR>
            ))}
            <EmptyRow count={items.length} cols={5} what="images" />
          </TBody>
        </Table>
      </Card>

      {pullOpen && (
        <PullImageDialog serverId={serverId} onClose={() => setPullOpen(false)} onDone={invalidate} />
      )}
    </div>
  );
}

function PullImageDialog({
  serverId, onClose, onDone,
}: { serverId: string; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [image, setImage] = useState("");
  const [error, setError] = useState("");

  const pull = useMutation({
    mutationFn: () => api(`/servers/${serverId}/docker/images`, { method: "POST", body: { image } }),
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "pull failed"),
  });

  return (
    <Dialog open onClose={onClose} title={t.docker.pullImage}>
      <form onSubmit={(e) => { e.preventDefault(); pull.mutate(); }} className="space-y-4">
        <Field label="Image reference">
          <Input autoFocus className="font-mono" value={image} onChange={(e) => setImage(e.target.value)}
            placeholder="nginx:alpine" />
        </Field>
        <p className="text-xs text-ink-dim">{t.docker.pullHint}</p>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!image.trim() || pull.isPending}>
            {pull.isPending ? "Pulling…" : "Pull"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function Volumes({ serverId }: { serverId: string }) {
  const t = useT();
  const { items, error, remove, prune, invalidate } = useResource<DockerVolume>(serverId, "volumes", "volumes");
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div className="space-y-3">
      <Toolbar
        count={items.length}
        label="volume"
        onPrune={() => { if (confirm(t.docker.pruneVolumesConfirm)) prune.mutate(); }}
        pruning={prune.isPending}
        action={<Button size="sm" onClick={() => setCreateOpen(true)}><Plus size={13} /> New volume</Button>}
      />
      <ErrorLine error={error} />
      <Card>
        <Table>
          <THead>
            <TR><TH>{t.common.name}</TH><TH>{t.docker.driver}</TH><TH>{t.docker.mountpoint}</TH><TH className="text-right">{t.common.actions}</TH></TR>
          </THead>
          <TBody>
            {items.map((v) => (
              <TR key={v.name}>
                <TD className="font-mono text-xs">{v.name}</TD>
                <TD className="text-xs">{v.driver}</TD>
                <TD className="max-w-96 truncate font-mono text-xs text-ink-dim" title={v.mountpoint}>{v.mountpoint}</TD>
                <TD className="text-right">
                  <Button size="sm" variant="ghost" title={t.common.remove}
                    onClick={() => { if (confirm(t.docker.confirmRemoveVolume.replace("{name}", v.name))) remove.mutate({ id: v.name, force: true }); }}>
                    <Trash2 size={13} className="text-err" />
                  </Button>
                </TD>
              </TR>
            ))}
            <EmptyRow count={items.length} cols={4} what="volumes" />
          </TBody>
        </Table>
      </Card>

      {createOpen && (
        <SimpleCreateDialog
          title={t.docker.newVolume}
          serverId={serverId}
          kind="volumes"
          onClose={() => setCreateOpen(false)}
          onDone={invalidate}
        />
      )}
    </div>
  );
}

function Networks({ serverId }: { serverId: string }) {
  const t = useT();
  const { items, error, remove, prune, invalidate } = useResource<DockerNetwork>(serverId, "networks", "networks");
  const [createOpen, setCreateOpen] = useState(false);
  const builtin = (name: string) => ["bridge", "host", "none"].includes(name);

  return (
    <div className="space-y-3">
      <Toolbar
        count={items.length}
        label="network"
        onPrune={() => { if (confirm(t.docker.pruneNetworksConfirm)) prune.mutate(); }}
        pruning={prune.isPending}
        action={<Button size="sm" onClick={() => setCreateOpen(true)}><Plus size={13} /> New network</Button>}
      />
      <ErrorLine error={error} />
      <Card>
        <Table>
          <THead>
            <TR><TH>{t.common.name}</TH><TH>{t.docker.driver}</TH><TH>{t.docker.scope}</TH><TH>{t.docker.containers}</TH><TH className="text-right">{t.common.actions}</TH></TR>
          </THead>
          <TBody>
            {items.map((n) => (
              <TR key={n.id}>
                <TD className="font-mono text-xs">
                  {n.name}
                  {n.internal && <Badge className="ml-2 border-edge bg-card text-ink-dim">internal</Badge>}
                </TD>
                <TD className="text-xs">{n.driver}</TD>
                <TD className="text-xs">{n.scope}</TD>
                <TD className="text-xs">{n.containers}</TD>
                <TD className="text-right">
                  <Button size="sm" variant="ghost" title={builtin(n.name) ? "Built-in network" : "Remove"}
                    disabled={builtin(n.name)}
                    onClick={() => { if (confirm(t.docker.confirmRemoveNetwork.replace("{name}", n.name))) remove.mutate({ id: n.id }); }}>
                    <Trash2 size={13} className={builtin(n.name) ? "text-ink-dim" : "text-err"} />
                  </Button>
                </TD>
              </TR>
            ))}
            <EmptyRow count={items.length} cols={5} what="networks" />
          </TBody>
        </Table>
      </Card>

      {createOpen && (
        <SimpleCreateDialog
          title={t.docker.newNetwork}
          serverId={serverId}
          kind="networks"
          withInternal
          onClose={() => setCreateOpen(false)}
          onDone={invalidate}
        />
      )}
    </div>
  );
}

function SimpleCreateDialog({
  title, serverId, kind, withInternal, onClose, onDone,
}: {
  title: string;
  serverId: string;
  kind: string;
  withInternal?: boolean;
  onClose: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [driver, setDriver] = useState("");
  const [internal, setInternal] = useState(false);
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => api(`/servers/${serverId}/docker/${kind}`, {
      method: "POST",
      body: { name, driver: driver.trim() || undefined, internal },
    }),
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  return (
    <Dialog open onClose={onClose} title={title}>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
        <Field label="Name">
          <Input autoFocus className="font-mono" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={`Driver (optional, default ${kind === "volumes" ? "local" : "bridge"})`}>
          <Input className="font-mono" value={driver} onChange={(e) => setDriver(e.target.value)} />
        </Field>
        {withInternal && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="accent-brand" checked={internal}
              onChange={(e) => setInternal(e.target.checked)} />
            Internal (no external connectivity)
          </label>
        )}
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!name.trim() || create.isPending}>{t.common.create}</Button>
        </div>
      </form>
    </Dialog>
  );
}

function Toolbar({
  count, label, onPrune, pruning, action,
}: {
  count: number;
  label: string;
  onPrune: () => void;
  pruning: boolean;
  action: React.ReactNode;
}) {
  const t = useT();
  return (
    <div className="flex items-center justify-between">
      <span className="text-sm text-ink-dim">{count} {label}{count === 1 ? "" : "s"}</span>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={onPrune} disabled={pruning}>
          <Eraser size={13} /> Prune unused
        </Button>
        {action}
      </div>
    </div>
  );
}

function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null;
  return <p className="text-sm text-err">{error instanceof ApiError ? error.message : "request failed"}</p>;
}

function EmptyRow({ count, cols, what }: { count: number; cols: number; what: string }) {
  if (count > 0) return null;
  return <TR><TD colSpan={cols} className="py-8 text-center text-ink-dim">No {what}.</TD></TR>;
}

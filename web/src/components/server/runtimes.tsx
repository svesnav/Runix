"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { Play, Plus, RotateCw, Square } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { runtimePath } from "@/lib/routes";
import { stateBadge, timeAgo } from "@/lib/format";
import type { RuntimeInfo } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import {
  DaemonForm, DaemonFormValue, daemonFormToConfig, emptyDaemonForm,
} from "@/components/server/daemon-form";
import { isRunning, useRuntimeActions } from "@/components/server/use-runtime-actions";
import { ComposeEditor, ComposeHint, SAMPLE_COMPOSE } from "@/components/server/compose-editor";
import { useT } from "@/i18n";

// Sub-tabs, one per provider type. Availability comes from the server's
// reported runtimeTypes; a type absent there shows an "unavailable" note.
const TYPES = [
  { id: "daemon", labelKey: "daemons" as const },
  { id: "docker", labelKey: "docker" as const },
  { id: "compose", labelKey: "compose" as const },
  { id: "systemd", labelKey: "systemd" as const },
];

export function RuntimesTab({
  serverId,
  online,
  availableTypes,
}: {
  serverId: string;
  online: boolean;
  availableTypes: string[];
}) {
  const t = useT();
  const [type, setType] = useState("daemon");

  if (!online) {
    return <p className="py-8 text-center text-sm text-ink-dim">{t.runtimes.agentOffline}</p>;
  }

  const tabs = TYPES.map((ty) => ({ id: ty.id, label: t.runtimes[ty.labelKey] }));

  return (
    <div className="space-y-4">
      <Tabs items={tabs} value={type} onChange={setType} />
      {availableTypes.includes(type) ? (
        <RuntimeTypePanel serverId={serverId} type={type} />
      ) : (
        <p className="py-8 text-center text-sm text-ink-dim">
          <span className="font-mono">{type}</span> — {t.runtimes.unavailable}
        </p>
      )}
    </div>
  );
}

function RuntimeTypePanel({ serverId, type }: { serverId: string; type: string }) {
  const t = useT();
  const router = useRouter();
  const [createOpen, setCreateOpen] = useState(false);
  const { action } = useRuntimeActions(serverId);

  const { data, error } = useQuery({
    queryKey: ["runtimes", serverId, type],
    queryFn: () => api<{ runtimes: RuntimeInfo[] }>(`/servers/${serverId}/runtimes`, { query: { type } }),
    refetchInterval: 10_000,
  });

  const items = useMemo(
    () => [...(data?.runtimes ?? [])].sort((a, b) => a.descriptor.name.localeCompare(b.descriptor.name)),
    [data],
  );

  const creatable = type === "daemon" || type === "docker" || type === "compose";
  const open = (rt: RuntimeInfo) =>
    router.push(runtimePath(serverId, type, rt.descriptor.id));

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <span className="text-sm text-ink-dim">
          {items.length} {t.runtimes.countSuffix}
        </span>
        {creatable && (
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus size={13} />{" "}
            {type === "daemon" ? t.runtimes.newDaemon
              : type === "compose" ? t.runtimes.newProject
                : t.runtimes.newContainer}
          </Button>
        )}
      </div>

      {error && <p className="text-sm text-err">{error instanceof ApiError ? error.message : "failed to load"}</p>}

      <Card>
        <Table>
          <THead>
            <TR><TH>{t.common.name}</TH><TH>{t.runtimes.state}</TH><TH>{t.runtimes.detail}</TH><TH>{t.runtimes.restarts}</TH><TH className="text-right">{t.common.actions}</TH></TR>
          </THead>
          <TBody>
            {items.map((rt) => {
              const d = rt.descriptor;
              const running = isRunning(d.status.state);
              return (
                <TR key={d.id} className="cursor-pointer hover:bg-card/50" onClick={() => open(rt)}>
                  <TD className="max-w-72 truncate font-mono text-xs" title={d.name}>{d.name}</TD>
                  <TD><Badge className={stateBadge(d.status.state)}>{d.status.state}</Badge></TD>
                  <TD className="max-w-80 truncate text-xs text-ink-dim" title={d.status.message}>
                    {d.status.message || (d.status.startedAt ? `up ${timeAgo(d.status.startedAt)}` : "—")}
                  </TD>
                  <TD className="text-xs">{d.status.restartCount || 0}</TD>
                  <TD onClick={(e) => e.stopPropagation()}>
                    <div className="flex justify-end gap-1">
                      {running ? (
                        <>
                          <Button size="sm" variant="ghost" title={t.common.restart}
                            onClick={() => action.mutate({ type, id: d.id, action: "restart" })}>
                            <RotateCw size={13} />
                          </Button>
                          <Button size="sm" variant="ghost" title={t.common.stop}
                            onClick={() => action.mutate({ type, id: d.id, action: "stop" })}>
                            <Square size={13} />
                          </Button>
                        </>
                      ) : (
                        rt.capabilities.includes("start") && (
                          <Button size="sm" variant="ghost" title={t.common.start}
                            onClick={() => action.mutate({ type, id: d.id, action: "start" })}>
                            <Play size={13} />
                          </Button>
                        )
                      )}
                      <Button size="sm" variant="outline" onClick={() => open(rt)}>{t.common.open}</Button>
                    </div>
                  </TD>
                </TR>
              );
            })}
            {data && items.length === 0 && (
              <TR><TD colSpan={5} className="py-8 text-center text-ink-dim">{t.runtimes.empty}</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      {createOpen && type === "daemon" && (
        <CreateDaemonDialog serverId={serverId} onClose={() => setCreateOpen(false)} />
      )}
      {createOpen && type === "docker" && (
        <CreateDockerDialog serverId={serverId} onClose={() => setCreateOpen(false)} />
      )}
      {createOpen && type === "compose" && (
        <CreateComposeDialog serverId={serverId} onClose={() => setCreateOpen(false)} />
      )}
    </div>
  );
}

function CreateDaemonDialog({ serverId, onClose }: { serverId: string; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<DaemonFormValue>(emptyDaemonForm());
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/runtimes/daemon`, {
        method: "POST",
        body: { name: form.name, config: daemonFormToConfig(form) },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runtimes", serverId] });
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  const valid = form.name.trim() && form.command.trim();

  return (
    <Dialog open onClose={onClose} title={t.runtimes.newDaemon} wide>
      <div className="space-y-4">
        <DaemonForm value={form} onChange={setForm} editing={false} />
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button onClick={() => create.mutate()} disabled={!valid || create.isPending}>{t.runtimes.newDaemon}</Button>
        </div>
      </div>
    </Dialog>
  );
}

function CreateComposeDialog({ serverId, onClose }: { serverId: string; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [dir, setDir] = useState("");
  const [content, setContent] = useState(SAMPLE_COMPOSE);
  const [up, setUp] = useState(true);
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/runtimes/compose`, {
        method: "POST",
        body: { name, config: { dir: dir.trim() || undefined, content, up } },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runtimes", serverId] });
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  return (
    <Dialog open onClose={onClose} title={t.runtimes.newProject} wide>
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.runtimes.composeForm.projectName}>
            <Input className="font-mono" value={name} onChange={(e) => setName(e.target.value.toLowerCase())}
              placeholder="my-stack" />
          </Field>
          <Field label={t.runtimes.composeForm.directory}>
            <Input className="font-mono" value={dir} onChange={(e) => setDir(e.target.value)}
              placeholder="/opt/my-stack" />
          </Field>
        </div>
        <ComposeEditor value={content} onChange={setContent} />
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="accent-brand" checked={up} onChange={(e) => setUp(e.target.checked)} />
          Start the project immediately (docker compose up -d)
        </label>
        <ComposeHint dir={dir.trim() || undefined} />
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={() => create.mutate()} disabled={!name.trim() || !content.trim() || create.isPending}>
            {create.isPending ? t.common.saving : t.runtimes.newProject}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function CreateDockerDialog({ serverId, onClose }: { serverId: string; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [ports, setPorts] = useState("");
  const [envText, setEnvText] = useState("");
  const [stopCommand, setStopCommand] = useState("");
  const [stopSignal, setStopSignal] = useState("SIGTERM");
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => {
      const config = {
        image: image.trim(),
        ports: ports.split(",").map((p) => p.trim()).filter(Boolean),
        env: envText.split("\n").map((l) => l.trim()).filter(Boolean),
        restartPolicy: "unless-stopped",
        stopCommand: stopCommand.trim(),
        stopSignal,
      };
      return api(`/servers/${serverId}/runtimes/docker`, { method: "POST", body: { name, config } });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runtimes", serverId] });
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  return (
    <Dialog open onClose={onClose} title={t.runtimes.newContainer}>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.runtimes.dockerForm.containerName}><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web" /></Field>
          <Field label="Image"><Input value={image} onChange={(e) => setImage(e.target.value)} placeholder="nginx:alpine" className="font-mono" /></Field>
        </div>
        <Field label={t.runtimes.dockerForm.ports}>
          <Input value={ports} onChange={(e) => setPorts(e.target.value)} placeholder="8080:80, 8443:443" className="font-mono" />
        </Field>
        <Field label={t.runtimes.dockerForm.env}>
          <textarea
            className="h-24 w-full rounded-md border border-edge bg-panel px-3 py-2 font-mono text-xs text-ink focus:border-brand/60 focus:outline-none"
            value={envText}
            onChange={(e) => setEnvText(e.target.value)}
            placeholder={"TZ=UTC\nLOG_LEVEL=info"}
          />
        </Field>
        {/* Only settable at creation: Docker fixes both on the container,
            so changing them later means recreating it. */}
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.runtimes.daemonForm.stopCommand}>
            <Input
              className="font-mono"
              value={stopCommand}
              onChange={(e) => setStopCommand(e.target.value)}
              placeholder={t.runtimes.daemonForm.stopCommandPlaceholder}
            />
            <p className="mt-1 text-[11px] text-ink-dim">{t.runtimes.daemonForm.stopCommandHint}</p>
          </Field>
          <Field label={t.runtimes.daemonForm.stopSignal}>
            <Select value={stopSignal} onChange={(e) => setStopSignal(e.target.value)}>
              <option value="SIGTERM">SIGTERM — {t.runtimes.daemonForm.signalTerm}</option>
              <option value="SIGINT">SIGINT — {t.runtimes.daemonForm.signalInt}</option>
              <option value="SIGQUIT">SIGQUIT</option>
              <option value="SIGHUP">SIGHUP</option>
            </Select>
            <p className="mt-1 text-[11px] text-ink-dim">{t.runtimes.daemonForm.stopSignalHint}</p>
          </Field>
        </div>
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!name || !image || create.isPending}>
            {t.runtimes.dockerForm.createContainer}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

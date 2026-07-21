"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import {
  ChevronLeft, FolderOpen, KeyRound, Pencil, Play, Power, RotateCw, Square, Trash2, Zap,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatDate, stateBadge } from "@/lib/format";
import type { ComposeInspect, DaemonInspect, RuntimeInfo } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { RuntimeConsole } from "@/components/server/runtime-console";
import { isRunning, useRuntimeActions } from "@/components/server/use-runtime-actions";
import {
  DaemonForm, DaemonFormValue, daemonFormFromConfig, daemonFormToConfig,
} from "@/components/server/daemon-form";
import { ComposeEditor, ComposeHint } from "@/components/server/compose-editor";
import { RuntimeTerminal } from "@/components/server/runtime-terminal";
import { GrantDialog } from "@/components/grant-dialog";
import { useT } from "@/i18n";

export default function RuntimeDetailPage() {
  const { id: serverId, type, rid } = useParams<{ id: string; type: string; rid: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const decodedRid = decodeURIComponent(rid);
  const [editOpen, setEditOpen] = useState(false);
  const [grantOpen, setGrantOpen] = useState(false);
  const [pane, setPane] = useState<"logs" | "terminal">("logs");
  const t = useT();

  const { action, remove } = useRuntimeActions(serverId, () => {
    queryClient.invalidateQueries({ queryKey: ["runtime", serverId, type, decodedRid] });
  });

  const { data: info } = useQuery({
    queryKey: ["runtime", serverId, type, decodedRid],
    queryFn: () => api<RuntimeInfo>(`/servers/${serverId}/runtimes/${type}/${encodeURIComponent(decodedRid)}`),
    refetchInterval: 8_000,
  });

  const { data: inspect } = useQuery({
    queryKey: ["runtime-inspect", serverId, type, decodedRid],
    queryFn: () => api<Record<string, unknown>>(`/servers/${serverId}/runtimes/${type}/${encodeURIComponent(decodedRid)}/inspect`),
    enabled: info?.capabilities.includes("inspect") ?? false,
    retry: false,
  });

  if (!info) return <p className="text-sm text-ink-dim">Loading…</p>;

  const d = info.descriptor;
  const caps = info.capabilities;
  const running = isRunning(d.status.state);
  const filesDir = runtimeFilesDir(type, d.labels, inspect);

  const act = (a: string, signal?: string) => action.mutate({ type, id: decodedRid, action: a, signal });

  return (
    <div className="space-y-4">
      <Link href={`/servers/${serverId}?tab=runtimes`} className="inline-flex items-center gap-1 text-xs text-ink-dim hover:text-ink">
        <ChevronLeft size={14} /> Back to {d.type} runtimes
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="font-mono text-lg font-semibold">{d.name}</h1>
            <Badge className={stateBadge(d.status.state)}>{d.status.state}</Badge>
            {d.status.health !== "unknown" && (
              <Badge className={stateBadge(d.status.health)}>{d.status.health}</Badge>
            )}
          </div>
          <p className="mt-1 text-xs text-ink-dim">{d.type} · {d.status.message || "no detail"}</p>
        </div>

        <div className="flex flex-wrap gap-2">
          {running ? (
            <>
              <Button size="sm" variant="outline" onClick={() => act("restart")}><RotateCw size={13} /> {t.common.restart}</Button>
              <Button size="sm" variant="outline" onClick={() => act("stop")}><Square size={13} /> {t.common.stop}</Button>
            </>
          ) : (
            caps.includes("start") && (
              <Button size="sm" onClick={() => act("start")}><Play size={13} /> {t.common.start}</Button>
            )
          )}
          {caps.includes("kill") && running && (
            <Button size="sm" variant="outline" onClick={() => act("kill", "SIGKILL")}><Zap size={13} /> {t.common.kill}</Button>
          )}
          {type === "systemd" && (
            <>
              <Button size="sm" variant="outline" onClick={() => act("enable")}><Power size={13} /> {t.common.enable}</Button>
              <Button size="sm" variant="outline" onClick={() => act("disable")}>{t.common.disable}</Button>
            </>
          )}
          {caps.includes("update") && (
            <Button size="sm" variant="outline" onClick={() => setEditOpen(true)}><Pencil size={13} /> {t.common.edit}</Button>
          )}
          {filesDir && (
            <Link href={`/servers/${serverId}?tab=files&path=${encodeURIComponent(filesDir)}`}>
              <Button size="sm" variant="outline"><FolderOpen size={13} /> {t.runtimes.browseFiles}</Button>
            </Link>
          )}
          {/* Grant someone access to just this runtime, without hunting for
              its identifier on the Grants page. */}
          <Button size="sm" variant="outline" onClick={() => setGrantOpen(true)}>
            <KeyRound size={13} /> {t.grants.grantAccess}
          </Button>
          {caps.includes("remove") && (
            <Button size="sm" variant="danger"
              onClick={() => {
                if (confirm(`Remove ${type} runtime "${d.name}"?`)) {
                  remove.mutate({ type, id: decodedRid }, { onSuccess: () => router.replace(`/servers/${serverId}?tab=runtimes`) });
                }
              }}>
              <Trash2 size={13} /> {t.common.remove}
            </Button>
          )}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-4">
        <Info label={t.runtimes.restarts} value={String(d.status.restartCount ?? 0)} />
        <Info label={t.runtimes.started} value={formatDate(d.status.startedAt)} />
        <Info label={t.runtimes.exitCode} value={d.status.exitCode !== undefined ? String(d.status.exitCode) : "—"} />
        <Info label={t.runtimes.directory} value={filesDir || "—"} mono />
      </div>

      <Card>
        <CardHeader>
          <div className="flex gap-1">
            <button
              onClick={() => setPane("logs")}
              className={`cursor-pointer rounded px-2 py-1 text-sm ${pane === "logs" ? "bg-card text-ink" : "text-ink-dim hover:text-ink"}`}
            >
              {caps.includes("console") ? t.runtimes.console : t.runtimes.logs}
            </button>
            {caps.includes("terminal") && (
              <button
                onClick={() => setPane("terminal")}
                className={`cursor-pointer rounded px-2 py-1 text-sm ${pane === "terminal" ? "bg-card text-ink" : "text-ink-dim hover:text-ink"}`}
              >
                Terminal
              </button>
            )}
          </div>
        </CardHeader>
        <CardBody className="p-2">
          {pane === "terminal" ? (
            <RuntimeTerminal serverId={serverId} type={type} rid={decodedRid} />
          ) : caps.includes("logs") || caps.includes("console") ? (
            <RuntimeConsole
              serverId={serverId}
              type={type}
              rid={decodedRid}
              // Only offer an input line when the process actually reads
              // stdin, rather than showing a box that silently does nothing.
              interactive={caps.includes("console")}
            />
          ) : (
            <p className="p-4 text-sm text-ink-dim">This runtime does not expose logs.</p>
          )}
        </CardBody>
      </Card>

      {editOpen && type === "daemon" && (
        <EditDaemonDialog
          serverId={serverId}
          rid={decodedRid}
          inspect={inspect as unknown as DaemonInspect | undefined}
          onClose={() => setEditOpen(false)}
        />
      )}
      {grantOpen && (
        <GrantDialog
          preset={{
            scopeType: "runtime",
            serverId,
            runtimeType: type,
            // Use the descriptor's own id, not the URL parameter: Docker
            // accepts a name, a short id and a full id for the same
            // container, and a grant must be pinned to the identifier the
            // rest of the UI addresses it by.
            runtimeId: d.id,
            permission: "runtime.manage",
          }}
          onClose={() => setGrantOpen(false)}
        />
      )}

      {editOpen && type === "compose" && (
        <EditComposeDialog
          serverId={serverId}
          rid={decodedRid}
          inspect={inspect as unknown as ComposeInspect | undefined}
          onClose={() => setEditOpen(false)}
        />
      )}
    </div>
  );
}

function Info({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Card>
      <CardBody>
        <div className="text-[11px] uppercase tracking-wider text-ink-dim">{label}</div>
        <div className={`mt-0.5 truncate text-sm ${mono ? "font-mono text-xs" : ""}`} title={value}>{value}</div>
      </CardBody>
    </Card>
  );
}

// runtimeFilesDir derives the on-host directory whose files back a runtime,
// so the "Browse files" button can open the file manager there.
function runtimeFilesDir(
  type: string,
  labels: Record<string, string> | undefined,
  inspect: Record<string, unknown> | undefined,
): string | null {
  if (type === "daemon") {
    const wd = labels?.workingDir || (inspect?.workingDir as string | undefined);
    return wd || null;
  }
  if (type === "systemd") {
    const frag = inspect?.FragmentPath as string | undefined;
    if (frag) return frag.replace(/\/[^/]+$/, "") || "/";
    return null;
  }
  if (type === "compose") {
    const files = labels?.configFiles;
    if (files) return files.split(",")[0].replace(/\/[^/]+$/, "") || "/";
    return null;
  }
  return null;
}

function EditComposeDialog({
  serverId,
  rid,
  inspect,
  onClose,
}: {
  serverId: string;
  rid: string;
  inspect: ComposeInspect | undefined;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [content, setContent] = useState(inspect?.content ?? "");
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/runtimes/compose/${encodeURIComponent(rid)}`, {
        method: "PUT",
        body: { config: { content } },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runtime", serverId] });
      queryClient.invalidateQueries({ queryKey: ["runtime-inspect", serverId] });
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "update failed"),
  });

  return (
    <Dialog open onClose={onClose} title={`Edit compose — ${rid}`} wide>
      <div className="space-y-4">
        <ComposeEditor value={content} onChange={setContent} path={inspect?.composeFile} />
        <ComposeHint dir={inspect?.composeFile} />
        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={() => save.mutate()} disabled={!content.trim() || save.isPending}>
            {save.isPending ? "Applying…" : "Save & apply"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function EditDaemonDialog({
  serverId,
  rid,
  inspect,
  onClose,
}: {
  serverId: string;
  rid: string;
  inspect: DaemonInspect | undefined;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<DaemonFormValue | null>(
    inspect?.spec ? daemonFormFromConfig(rid, inspect.spec) : null,
  );
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/runtimes/daemon/${encodeURIComponent(rid)}`, {
        method: "PUT",
        body: { config: daemonFormToConfig(form!) },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runtime", serverId] });
      queryClient.invalidateQueries({ queryKey: ["runtimes", serverId] });
      onClose();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "update failed"),
  });

  return (
    <Dialog open onClose={onClose} title={`Edit daemon — ${rid}`} wide>
      {form ? (
        <div className="space-y-4">
          <DaemonForm value={form} onChange={setForm} editing />
          <p className="text-xs text-ink-dim">Saving restarts the daemon if it is running.</p>
          {error && <p className="text-xs text-err">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onClose}>Cancel</Button>
            <Button onClick={() => save.mutate()} disabled={!form.command.trim() || save.isPending}>Save changes</Button>
          </div>
        </div>
      ) : (
        <p className="text-sm text-ink-dim">Loading configuration…</p>
      )}
    </Dialog>
  );
}

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { History, Play, Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { formatDate, stateBadge, timeAgo } from "@/lib/format";
import type { ListResponse, ScheduledTask, Server, TaskRun } from "@/lib/types";
import { useT } from "@/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";

export default function SchedulePage() {
  const t = useT();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<ScheduledTask | "new" | null>(null);
  const [historyFor, setHistoryFor] = useState<ScheduledTask | null>(null);

  const { data } = useQuery({
    queryKey: ["scheduled-tasks"],
    queryFn: () => api<{ tasks: ScheduledTask[] }>("/scheduled-tasks"),
    refetchInterval: 20_000,
  });
  const { data: servers } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<ListResponse<Server>>("/servers", { query: { size: 200 } }),
  });

  const serverName = useMemo(() => {
    const map = new Map<string, string>();
    for (const s of servers?.items ?? []) map.set(s.id, s.name);
    return map;
  }, [servers]);

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["scheduled-tasks"] });

  const remove = useMutation({
    mutationFn: (id: string) => api(`/scheduled-tasks/${id}`, { method: "DELETE" }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "delete failed"),
  });

  const runNow = useMutation({
    mutationFn: (id: string) => api<TaskRun>(`/scheduled-tasks/${id}/run`, { method: "POST" }),
    onSuccess: (run) => {
      invalidate();
      alert(`${run.status}: ${run.detail || "—"}`);
    },
    onError: (err) => alert(err instanceof ApiError ? err.message : "run failed"),
  });

  const tasks = data?.tasks ?? [];

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold">{t.schedule.title}</h1>
          <p className="mt-1 text-xs text-ink-dim">{t.schedule.intro}</p>
        </div>
        <Button onClick={() => setEditing("new")}>
          <Plus size={15} /> {t.schedule.newTask}
        </Button>
      </div>

      <Card>
        <Table>
          <THead>
            <TR>
              <TH>{t.common.name}</TH><TH>{t.schedule.server}</TH><TH>{t.schedule.cron}</TH>
              <TH>{t.schedule.nextRun}</TH><TH>{t.schedule.lastRun}</TH>
              <TH className="text-right">{t.common.actions}</TH>
            </TR>
          </THead>
          <TBody>
            {tasks.map((task) => (
              <TR key={task.id}>
                <TD>
                  <div className="font-medium">{task.name}</div>
                  <div className="font-mono text-[11px] text-ink-dim">
                    {task.kind === "runtime_exec"
                      ? (task.payload.cmd ?? []).join(" ")
                      : `${task.payload.action} ${task.payload.runtimeType}/${task.payload.runtimeId}`}
                  </div>
                </TD>
                <TD className="text-xs">{serverName.get(task.serverId) ?? "—"}</TD>
                <TD className="font-mono text-xs">{task.cron}</TD>
                <TD className="text-xs text-ink-dim">
                  {task.enabled ? formatDate(task.nextRunAt) : <Badge className="border-edge bg-card text-ink-dim">{t.common.disable}d</Badge>}
                </TD>
                <TD className="text-xs">
                  {task.lastRunAt ? (
                    <span className="flex items-center gap-2">
                      <Badge className={stateBadge(task.lastStatus === "success" ? "success" : "failure")}>
                        {task.lastStatus}
                      </Badge>
                      <span className="text-ink-dim">{timeAgo(task.lastRunAt)}</span>
                    </span>
                  ) : (
                    <span className="text-ink-dim">{t.common.never}</span>
                  )}
                </TD>
                <TD>
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" title={t.schedule.runNow}
                      onClick={() => runNow.mutate(task.id)} disabled={runNow.isPending}>
                      <Play size={13} />
                    </Button>
                    <Button size="sm" variant="ghost" title={t.schedule.history}
                      onClick={() => setHistoryFor(task)}>
                      <History size={13} />
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setEditing(task)}>{t.common.edit}</Button>
                    <Button size="sm" variant="ghost" title={t.common.delete}
                      onClick={() => { if (confirm(t.schedule.deleteConfirm)) remove.mutate(task.id); }}>
                      <Trash2 size={13} className="text-err" />
                    </Button>
                  </div>
                </TD>
              </TR>
            ))}
            {data && tasks.length === 0 && (
              <TR><TD colSpan={6} className="py-8 text-center text-ink-dim">{t.schedule.empty}</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      {editing && (
        <TaskDialog
          task={editing === "new" ? null : editing}
          servers={servers?.items ?? []}
          onClose={() => setEditing(null)}
          onDone={invalidate}
        />
      )}
      {historyFor && <HistoryDialog task={historyFor} onClose={() => setHistoryFor(null)} />}
    </div>
  );
}

function TaskDialog({
  task, servers, onClose, onDone,
}: {
  task: ScheduledTask | null;
  servers: Server[];
  onClose: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(task?.name ?? "");
  const [serverId, setServerId] = useState(task?.serverId ?? "");
  const [kind, setKind] = useState(task?.kind ?? "runtime_action");
  const [runtimeType, setRuntimeType] = useState(task?.payload.runtimeType ?? "daemon");
  const [runtimeId, setRuntimeId] = useState(task?.payload.runtimeId ?? "");
  const [action, setAction] = useState(task?.payload.action ?? "restart");
  const [command, setCommand] = useState((task?.payload.cmd ?? []).join(" "));
  const [cron, setCron] = useState(task?.cron ?? "0 3 * * *");
  const [enabled, setEnabled] = useState(task?.enabled ?? true);
  const [error, setError] = useState("");

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name, serverId, kind, cron, enabled,
        payload: {
          runtimeType, runtimeId,
          action: kind === "runtime_action" ? action : undefined,
          cmd: kind === "runtime_exec" ? command.split(/\s+/).filter(Boolean) : undefined,
        },
      };
      return task
        ? api(`/scheduled-tasks/${task.id}`, { method: "PUT", body })
        : api("/scheduled-tasks", { method: "POST", body });
    },
    onSuccess: () => { onDone(); onClose(); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  const valid = name.trim() && serverId && runtimeType && runtimeId && cron.trim() &&
    (kind === "runtime_action" ? action : command.trim());

  return (
    <Dialog open onClose={onClose} title={task ? t.schedule.editTask : t.schedule.newTask}>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={t.schedule.taskName}>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="nightly-restart" />
          </Field>
          <Field label={t.schedule.server}>
            <Select value={serverId} onChange={(e) => setServerId(e.target.value)}>
              <option value="">{t.grants.select}</option>
              {servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </Select>
          </Field>
        </div>

        <Field label={t.schedule.kind}>
          <Select value={kind} onChange={(e) => setKind(e.target.value as ScheduledTask["kind"])}>
            <option value="runtime_action">{t.schedule.kindAction}</option>
            <option value="runtime_exec">{t.schedule.kindExec}</option>
          </Select>
        </Field>

        <div className="grid grid-cols-2 gap-4">
          <Field label={t.schedule.runtimeType}>
            <Select value={runtimeType} onChange={(e) => setRuntimeType(e.target.value)}>
              <option value="daemon">daemon</option>
              <option value="docker">docker</option>
              <option value="compose">compose</option>
              <option value="systemd">systemd</option>
            </Select>
          </Field>
          <Field label={t.schedule.runtimeId}>
            <Input className="font-mono" value={runtimeId} onChange={(e) => setRuntimeId(e.target.value)}
              placeholder="my-service" />
          </Field>
        </div>

        {kind === "runtime_action" ? (
          <Field label={t.schedule.action}>
            <Select value={action} onChange={(e) => setAction(e.target.value)}>
              <option value="start">start</option>
              <option value="stop">stop</option>
              <option value="restart">restart</option>
              <option value="reload">reload</option>
            </Select>
          </Field>
        ) : (
          <Field label={t.schedule.command}>
            <Input className="font-mono" value={command} onChange={(e) => setCommand(e.target.value)}
              placeholder={t.schedule.commandPlaceholder} />
          </Field>
        )}

        <Field label={t.schedule.cron}>
          <Input className="font-mono" value={cron} onChange={(e) => setCron(e.target.value)} placeholder="0 3 * * *" />
        </Field>
        <p className="text-[11px] text-ink-dim">{t.schedule.cronHint}</p>

        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="accent-brand" checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)} />
          {t.schedule.enabled}
        </label>

        {error && <p className="text-xs text-err">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
          <Button type="submit" disabled={!valid || save.isPending}>{t.common.save}</Button>
        </div>
      </form>
    </Dialog>
  );
}

function HistoryDialog({ task, onClose }: { task: ScheduledTask; onClose: () => void }) {
  const t = useT();
  const { data } = useQuery({
    queryKey: ["task-runs", task.id],
    queryFn: () => api<{ runs: TaskRun[] }>(`/scheduled-tasks/${task.id}/runs`),
  });
  const runs = data?.runs ?? [];

  return (
    <Dialog open onClose={onClose} title={`${t.schedule.history} — ${task.name}`} wide>
      <Table>
        <THead>
          <TR>
            <TH>{t.schedule.lastRun}</TH><TH>{t.schedule.duration}</TH>
            <TH>{t.schedule.result}</TH><TH>{t.schedule.detail}</TH>
          </TR>
        </THead>
        <TBody>
          {runs.map((run) => (
            <TR key={run.id}>
              <TD className="whitespace-nowrap text-xs">{formatDate(run.startedAt)}</TD>
              <TD className="text-xs">{run.durationMs} ms</TD>
              <TD>
                <Badge className={stateBadge(run.status === "success" ? "success" : "failure")}>
                  {run.status}
                </Badge>
              </TD>
              <TD className="max-w-96 truncate font-mono text-xs text-ink-dim" title={run.detail}>
                {run.detail || "—"}
              </TD>
            </TR>
          ))}
          {data && runs.length === 0 && (
            <TR><TD colSpan={4} className="py-6 text-center text-ink-dim">{t.schedule.noHistory}</TD></TR>
          )}
        </TBody>
      </Table>
    </Dialog>
  );
}

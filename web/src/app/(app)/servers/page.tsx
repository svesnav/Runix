"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Plus } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { serverPath } from "@/lib/routes";
import { stateBadge, timeAgo } from "@/lib/format";
import type { ListResponse, Server, ServerCreated } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { AgentInstall } from "@/components/agent-install";
import { GroupManager } from "@/components/group-manager";
import { useT } from "@/i18n";

export default function ServersPage() {
  const router = useRouter();
  const t = useT();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [location, setLocation] = useState("");
  const [agentToken, setAgentToken] = useState("");
  const [error, setError] = useState("");
  const [created, setCreated] = useState<ServerCreated | null>(null);

  const { data } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<ListResponse<Server>>("/servers", { query: { size: 200 } }),
    refetchInterval: 15_000,
  });

  const create = useMutation({
    mutationFn: () =>
      api<ServerCreated>("/servers", {
        method: "POST",
        body: { name, description, location, agentToken: agentToken.trim() || undefined },
      }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ["servers"] });
      setCreateOpen(false);
      setName(""); setDescription(""); setLocation(""); setAgentToken(""); setError("");
      setCreated(res);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "failed"),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">{t.servers.title}</h1>
        <Button onClick={() => setCreateOpen(true)}>
          <Plus size={15} /> {t.servers.add}
        </Button>
      </div>

      <Card>
        <Table>
          <THead>
            <TR>
              <TH>{t.common.name}</TH><TH>{t.common.status}</TH><TH>{t.servers.hostname}</TH>
              <TH>{t.servers.osArch}</TH><TH>{t.servers.runtimes}</TH>
              <TH>{t.servers.agent}</TH><TH>{t.servers.lastSeen}</TH>
            </TR>
          </THead>
          <TBody>
            {(data?.items ?? []).map((srv) => (
              <TR
                key={srv.id}
                className="cursor-pointer hover:bg-card/60"
                onClick={() => router.push(serverPath(srv.id))}
              >
                <TD className="font-medium">{srv.name}</TD>
                <TD><Badge className={stateBadge(srv.connectionStatus)}>{srv.connectionStatus.replace("_", " ")}</Badge></TD>
                <TD className="font-mono text-xs">{srv.hostname || "—"}</TD>
                <TD className="text-xs">{srv.os ? `${srv.os} / ${srv.architecture}` : "—"}</TD>
                <TD>
                  <div className="flex gap-1">
                    {srv.runtimeTypes.map((t) => (
                      <Badge key={t} className="border-edge bg-card text-ink-dim">{t}</Badge>
                    ))}
                  </div>
                </TD>
                <TD className="text-xs">{srv.agentVersion || "—"}</TD>
                <TD className="text-xs text-ink-dim">{timeAgo(srv.lastSeenAt)}</TD>
              </TR>
            ))}
            {data && data.items.length === 0 && (
              <TR><TD colSpan={7} className="py-8 text-center text-ink-dim">{t.servers.empty}</TD></TR>
            )}
          </TBody>
        </Table>
      </Card>

      <GroupManager flavour="servers" />

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title={t.servers.addTitle}>
        <form
          onSubmit={(e) => { e.preventDefault(); create.mutate(); }}
          className="space-y-4"
        >
          <div className="grid gap-4">
            <Field label="Name">
              <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="prod-web-01" />
            </Field>
          </div>
          <Field label="Description">
            <Textarea rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <Field label="Location">
            <Input value={location} onChange={(e) => setLocation(e.target.value)} placeholder="eu-central / rack 4" />
          </Field>
          <Field label="Agent token (optional — generated if left empty)">
            <Input
              className="font-mono"
              value={agentToken}
              onChange={(e) => setAgentToken(e.target.value)}
              placeholder={t.servers.agentTokenPlaceholder}
            />
          </Field>
          {error && <p className="text-xs text-err">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>{t.common.cancel}</Button>
            <Button type="submit" disabled={!name || create.isPending}>{t.common.create}</Button>
          </div>
        </form>
      </Dialog>

      <Dialog open={created !== null} onClose={() => setCreated(null)} title={t.servers.installTitle}>
        {created && (
          <div className="space-y-3">
            <p className="text-sm text-ink-dim">
              <b className="text-ink">{created.server.name}</b> is registered. Install the agent with the
              command below — the token is shown <b className="text-ink">once</b>.
            </p>
            <AgentInstall token={created.agentToken} />
          </div>
        )}
      </Dialog>
    </div>
  );
}

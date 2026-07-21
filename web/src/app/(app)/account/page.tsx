"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "next/navigation";
import { QRCodeSVG } from "qrcode.react";
import { Suspense, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { formatDate, timeAgo } from "@/lib/format";
import type { CreatedPAT, MeResponse, PAT, Session } from "@/lib/types";
import { useAuth } from "@/stores/auth";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { TokenReveal } from "@/components/token-reveal";

export default function AccountPage() {
  return (
    <Suspense>
      <AccountContent />
    </Suspense>
  );
}

function AccountContent() {
  const forceChange = useSearchParams().get("forceChange") === "1";
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api<MeResponse>("/me") });

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-lg font-semibold">Account</h1>
      {forceChange && (
        <p className="rounded-md border border-warn/40 bg-warn/10 px-4 py-3 text-sm text-warn">
          Your password must be changed before continuing.
        </p>
      )}
      <PasswordSection />
      <MFASection enabled={me?.user.totpEnabled ?? false} />
      <SessionsSection />
      <TokensSection />
    </div>
  );
}

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);

  const change = useMutation({
    mutationFn: () =>
      api("/me/password", { method: "PUT", body: { currentPassword: current, newPassword: next } }),
    onSuccess: () => {
      setCurrent(""); setNext("");
      setMessage({ ok: true, text: "password changed" });
    },
    onError: (err) =>
      setMessage({ ok: false, text: err instanceof ApiError ? err.message : "change failed" }),
  });

  return (
    <Card>
      <CardHeader><CardTitle>Password</CardTitle></CardHeader>
      <CardBody>
        <form
          onSubmit={(e) => { e.preventDefault(); change.mutate(); }}
          className="grid gap-4 sm:grid-cols-2"
        >
          <Field label="Current password">
            <Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </Field>
          <Field label="New password (min 10 characters)">
            <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} />
          </Field>
          <div className="sm:col-span-2 flex items-center justify-between">
            {message
              ? <span className={`text-xs ${message.ok ? "text-ok" : "text-err"}`}>{message.text}</span>
              : <span />}
            <Button type="submit" disabled={!current || !next || change.isPending}>Change password</Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}

function MFASection({ enabled }: { enabled: boolean }) {
  const queryClient = useQueryClient();
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  const refreshMe = () => queryClient.invalidateQueries({ queryKey: ["me"] });

  const begin = useMutation({
    mutationFn: () => api<{ secret: string; uri: string }>("/me/mfa/setup", { method: "POST" }),
    onSuccess: (res) => { setSetup(res); setError(""); },
    onError: (err) => setError(err instanceof ApiError ? err.message : "setup failed"),
  });

  const enable = useMutation({
    mutationFn: () => api<{ recoveryCodes: string[] }>("/me/mfa/enable", { method: "POST", body: { code } }),
    onSuccess: (res) => {
      setRecovery(res.recoveryCodes);
      setSetup(null); setCode(""); setError("");
      refreshMe();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "invalid code"),
  });

  const disable = useMutation({
    mutationFn: () => api("/me/mfa/disable", { method: "POST", body: { password, code } }),
    onSuccess: () => {
      setDisableOpen(false); setPassword(""); setCode(""); setError("");
      refreshMe();
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "disable failed"),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Two-factor authentication</CardTitle>
        <span className={`text-xs ${enabled ? "text-ok" : "text-ink-dim"}`}>
          {enabled ? "enabled (TOTP)" : "disabled"}
        </span>
      </CardHeader>
      <CardBody className="space-y-4">
        {!enabled && !setup && (
          <Button variant="outline" onClick={() => begin.mutate()} disabled={begin.isPending}>
            Set up authenticator app
          </Button>
        )}

        {setup && (
          <div className="flex flex-col gap-4 sm:flex-row">
            <div className="rounded-md bg-white p-3">
              <QRCodeSVG value={setup.uri} size={144} />
            </div>
            <div className="flex-1 space-y-3">
              <p className="text-sm text-ink-dim">
                Scan with your authenticator app, or enter the secret manually:
              </p>
              <code className="block break-all rounded-md border border-edge bg-canvas p-2 font-mono text-xs">
                {setup.secret}
              </code>
              <form
                onSubmit={(e) => { e.preventDefault(); enable.mutate(); }}
                className="flex gap-2"
              >
                <Input
                  className="max-w-40"
                  placeholder="6-digit code"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
                <Button type="submit" disabled={!code || enable.isPending}>Verify & enable</Button>
              </form>
            </div>
          </div>
        )}

        {enabled && (
          <Button variant="danger" onClick={() => setDisableOpen(true)}>Disable 2FA</Button>
        )}
        {error && <p className="text-xs text-err">{error}</p>}
      </CardBody>

      <Dialog open={recovery !== null} onClose={() => setRecovery(null)} title="Recovery codes">
        <p className="mb-3 text-sm text-ink-dim">
          Each code works once if you lose your authenticator. They are shown{" "}
          <b className="text-ink">only now</b> — store them safely.
        </p>
        <div className="grid grid-cols-2 gap-2 rounded-md border border-edge bg-canvas p-4 font-mono text-sm">
          {(recovery ?? []).map((c) => <span key={c}>{c}</span>)}
        </div>
      </Dialog>

      <Dialog open={disableOpen} onClose={() => setDisableOpen(false)} title="Disable two-factor authentication">
        <form onSubmit={(e) => { e.preventDefault(); disable.mutate(); }} className="space-y-4">
          <Field label="Password">
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Field label="Current authenticator code">
            <Input value={code} onChange={(e) => setCode(e.target.value)} />
          </Field>
          {error && <p className="text-xs text-err">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setDisableOpen(false)}>Cancel</Button>
            <Button type="submit" variant="danger" disabled={!password || !code || disable.isPending}>
              Disable
            </Button>
          </div>
        </form>
      </Dialog>
    </Card>
  );
}

function SessionsSection() {
  const queryClient = useQueryClient();
  const currentSession = useAuth((s) => s.refreshToken);
  const { data } = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api<{ sessions: Session[] }>("/me/sessions"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api(`/me/sessions/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  });

  return (
    <Card>
      <CardHeader><CardTitle>Active sessions</CardTitle></CardHeader>
      <Table>
        <THead>
          <TR><TH>Client</TH><TH>IP</TH><TH>Last used</TH><TH>Expires</TH><TH className="text-right" /></TR>
        </THead>
        <TBody>
          {(data?.sessions ?? []).map((s) => (
            <TR key={s.id}>
              <TD className="max-w-72 truncate text-xs" title={s.userAgent}>{s.userAgent || "unknown"}</TD>
              <TD className="font-mono text-xs">{s.ip}</TD>
              <TD className="text-xs text-ink-dim">{timeAgo(s.lastUsedAt)}</TD>
              <TD className="text-xs text-ink-dim">{formatDate(s.expiresAt)}</TD>
              <TD className="text-right">
                <Button size="sm" variant="ghost" onClick={() => revoke.mutate(s.id)}>Revoke</Button>
              </TD>
            </TR>
          ))}
        </TBody>
      </Table>
      {currentSession && (
        <p className="border-t border-edge px-4 py-2 text-[11px] text-ink-dim">
          Revoking your current session signs you out on next token refresh.
        </p>
      )}
    </Card>
  );
}

function TokensSection() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<CreatedPAT | null>(null);
  const [error, setError] = useState("");

  const { data } = useQuery({
    queryKey: ["pats"],
    queryFn: () => api<{ tokens: PAT[] }>("/me/tokens"),
  });

  const create = useMutation({
    mutationFn: () => api<CreatedPAT>("/me/tokens", { method: "POST", body: { name } }),
    onSuccess: (res) => {
      setCreated(res); setCreateOpen(false); setName(""); setError("");
      queryClient.invalidateQueries({ queryKey: ["pats"] });
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "create failed"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api(`/me/tokens/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["pats"] }),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>API tokens</CardTitle>
        <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>New token</Button>
      </CardHeader>
      <Table>
        <THead>
          <TR><TH>Name</TH><TH>Created</TH><TH>Last used</TH><TH className="text-right" /></TR>
        </THead>
        <TBody>
          {(data?.tokens ?? []).map((t) => (
            <TR key={t.id}>
              <TD className="text-sm font-medium">{t.name}</TD>
              <TD className="text-xs text-ink-dim">{formatDate(t.createdAt)}</TD>
              <TD className="text-xs text-ink-dim">{t.lastUsedAt ? timeAgo(t.lastUsedAt) : "never"}</TD>
              <TD className="text-right">
                <Button size="sm" variant="ghost" onClick={() => revoke.mutate(t.id)}>Revoke</Button>
              </TD>
            </TR>
          ))}
          {data && data.tokens.length === 0 && (
            <TR><TD colSpan={4} className="py-6 text-center text-ink-dim">No API tokens</TD></TR>
          )}
        </TBody>
      </Table>

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="New API token">
        <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }} className="space-y-4">
          <Field label="Token name">
            <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-deploys" />
          </Field>
          {error && <p className="text-xs text-err">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button type="submit" disabled={!name || create.isPending}>Create</Button>
          </div>
        </form>
      </Dialog>

      <Dialog open={created !== null} onClose={() => setCreated(null)} title="API token created">
        {created && (
          <div className="space-y-3">
            <p className="text-sm text-ink-dim">
              Use it as <code className="text-ink">Authorization: Bearer …</code> — shown once.
            </p>
            <TokenReveal token={created.plainToken} />
          </div>
        )}
      </Dialog>
    </Card>
  );
}

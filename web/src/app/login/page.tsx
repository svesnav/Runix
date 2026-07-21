"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { LoginResponse } from "@/lib/types";
import { useAuth, useAuthHydrated } from "@/stores/auth";
import { useT } from "@/i18n";
import { LanguageSwitcher } from "@/components/language-switcher";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export default function LoginPage() {
  const router = useRouter();
  const t = useT();
  const setSession = useAuth((s) => s.setSession);
  const hydrated = useAuthHydrated();
  const existingSession = useAuth((s) => s.refreshToken);

  useEffect(() => {
    if (hydrated && existingSession) router.replace("/dashboard");
  }, [hydrated, existingSession, router]);

  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(false);
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const finish = (res: LoginResponse) => {
    if (res.tokens && res.user) {
      setSession(res.tokens, res.user);
      router.replace(res.user.mustChangePassword ? "/account?forceChange=1" : "/dashboard");
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (mfaToken) {
        finish(await api<LoginResponse>("/auth/mfa/verify", {
          method: "POST",
          body: { mfaToken, code, remember },
        }));
      } else {
        const res = await api<LoginResponse>("/auth/login", {
          method: "POST",
          body: { identifier, password, remember },
        });
        if (res.mfaRequired && res.mfaToken) {
          setMfaToken(res.mfaToken);
        } else {
          finish(res);
        }
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t.login.failed);
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <form onSubmit={submit} className="w-full max-w-sm rounded-lg border border-edge bg-panel p-6">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-bold tracking-tight">
            Run<span className="text-brand">ix</span>
          </h1>
          <p className="mt-1 text-xs text-ink-dim">{t.login.subtitle}</p>
        </div>

        {mfaToken ? (
          <div className="space-y-4">
            <Field label={t.login.mfaCode}>
              <Input
                autoFocus
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder={t.login.mfaPlaceholder}
              />
            </Field>
            <Button type="submit" disabled={busy || !code} className="w-full">
              {t.login.verify}
            </Button>
            <button
              type="button"
              onClick={() => { setMfaToken(null); setCode(""); }}
              className="w-full cursor-pointer text-center text-xs text-ink-dim hover:text-ink"
            >
              {t.login.backToLogin}
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            <Field label={t.login.identifier}>
              <Input autoFocus value={identifier} onChange={(e) => setIdentifier(e.target.value)} />
            </Field>
            <Field label={t.login.password}>
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            <label className="flex items-center gap-2 text-xs text-ink-dim">
              <input
                type="checkbox"
                checked={remember}
                onChange={(e) => setRemember(e.target.checked)}
                className="accent-brand"
              />
              {t.login.remember}
            </label>
            <Button type="submit" disabled={busy || !identifier || !password} className="w-full">
              {busy ? t.login.signingIn : t.login.signIn}
            </Button>
          </div>
        )}

        {error && <p className="mt-4 text-center text-xs text-err">{error}</p>}

        <div className="mt-6 border-t border-edge pt-3">
          <LanguageSwitcher />
        </div>
      </form>
    </main>
  );
}

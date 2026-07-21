"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect } from "react";
import { clsx } from "clsx";
import {
  Activity, CalendarClock, FileClock, KeyRound, LayoutDashboard, LogOut, Server,
  Settings, Shield, Users,
} from "lucide-react";
import { api } from "@/lib/api";
import { useT } from "@/i18n";
import { LanguageSwitcher } from "@/components/language-switcher";
import { useAuth, useAuthHydrated } from "@/stores/auth";

// Labels come from the dictionary at render time so the nav follows the
// selected language.
const nav = [
  { href: "/dashboard", key: "dashboard", icon: LayoutDashboard },
  { href: "/servers", key: "servers", icon: Server },
  { href: "/schedule", key: "schedule", icon: CalendarClock },
  { href: "/users", key: "users", icon: Users },
  { href: "/roles", key: "roles", icon: Shield },
  { href: "/grants", key: "grants", icon: KeyRound },
  { href: "/audit", key: "audit", icon: FileClock },
  { href: "/settings", key: "settings", icon: Settings },
] as const;

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const hydrated = useAuthHydrated();
  const t = useT();
  const { refreshToken, user, clear } = useAuth();

  useEffect(() => {
    if (hydrated && !refreshToken) router.replace("/login");
  }, [hydrated, refreshToken, router]);

  if (!hydrated || !refreshToken) return null;

  const logout = async () => {
    try {
      await api("/auth/logout", { method: "POST", body: { refreshToken } });
    } catch {
      // Session revocation is best-effort; clear locally regardless.
    }
    clear();
    router.replace("/login");
  };

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-52 shrink-0 flex-col border-r border-edge bg-panel">
        <div className="flex h-14 items-center gap-2 border-b border-edge px-4">
          <Activity size={18} className="text-brand" />
          <span className="text-base font-bold tracking-tight">
            Run<span className="text-brand">ix</span>
          </span>
        </div>
        <nav className="flex-1 space-y-0.5 p-2">
          {nav.map(({ href, key, icon: Icon }) => (
            <Link
              key={href}
              href={href}
              className={clsx(
                "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors",
                pathname.startsWith(href)
                  ? "bg-card text-ink"
                  : "text-ink-dim hover:bg-card/60 hover:text-ink",
              )}
            >
              <Icon size={15} />
              {t.nav[key]}
            </Link>
          ))}
        </nav>
        <div className="border-t border-edge p-2">
          <Link
            href="/account"
            className={clsx(
              "block rounded-md px-3 py-2 text-sm",
              pathname.startsWith("/account")
                ? "bg-card text-ink"
                : "text-ink-dim hover:bg-card/60 hover:text-ink",
            )}
          >
            <div className="truncate font-medium">{user?.displayName || user?.username}</div>
            <div className="truncate text-[11px] text-ink-dim">{user?.email}</div>
          </Link>
          <LanguageSwitcher />
          <button
            onClick={logout}
            className="mt-1 flex w-full cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm text-ink-dim hover:bg-card/60 hover:text-ink"
          >
            <LogOut size={15} />
            {t.nav.signOut}
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-6">{children}</main>
    </div>
  );
}

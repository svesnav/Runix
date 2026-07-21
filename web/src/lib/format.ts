import type { RuntimeState } from "@/lib/types";

export function formatBytes(n: number | undefined): string {
  if (!n || n <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function formatUptime(secs: number | undefined): string {
  if (!secs || secs <= 0) return "—";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function timeAgo(iso: string | undefined): string {
  if (!iso) return "never";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0) return "now";
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function formatDate(iso: string | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString();
}

// stateBadge maps runtime/connection states onto badge color classes.
export function stateBadge(state: RuntimeState | string): string {
  switch (state) {
    case "running":
    case "online":
    case "healthy":
    case "success":
      return "bg-ok/15 text-ok border-ok/30";
    case "degraded":
    case "paused":
    case "starting":
    case "stopping":
    case "unhealthy":
      return "bg-warn/15 text-warn border-warn/30";
    case "failed":
    case "offline":
    case "failure":
      return "bg-err/15 text-err border-err/30";
    default:
      return "bg-ink-dim/10 text-ink-dim border-edge";
  }
}

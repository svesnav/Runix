import { useAuth } from "@/stores/auth";
import type { TokenPair } from "@/lib/types";

const BASE = process.env.NEXT_PUBLIC_API_URL ?? "";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

let refreshInFlight: Promise<boolean> | null = null;

async function refreshSession(): Promise<boolean> {
  const { refreshToken } = useAuth.getState();
  if (!refreshToken) return false;
  try {
    const res = await fetch(`${BASE}/api/v1/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refreshToken }),
    });
    if (!res.ok) return false;
    const pair = (await res.json()) as TokenPair;
    useAuth.getState().setTokens(pair.accessToken, pair.refreshToken);
    return true;
  } catch {
    return false;
  }
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined>;
}

// apiURL builds an absolute API URL; used by transfers that bypass the JSON
// helper (downloads via anchor, uploads via XHR for progress).
export function apiURL(path: string, query: Record<string, string | string[] | boolean | undefined> = {}): string {
  const base = BASE || (typeof window !== "undefined" ? window.location.origin : "http://localhost");
  const url = new URL(`/api/v1${path}`, base);
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === "") continue;
    if (Array.isArray(v)) {
      for (const item of v) url.searchParams.append(k, item);
    } else {
      url.searchParams.set(k, String(v));
    }
  }
  return url.toString();
}

// authFetch is a raw fetch with the bearer token attached, for binary
// responses (downloads) where the JSON envelope does not apply.
export async function authFetch(url: string, init: RequestInit = {}): Promise<Response> {
  const { accessToken } = useAuth.getState();
  const headers = new Headers(init.headers);
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
  return fetch(url, { ...init, headers });
}

export async function api<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const attempt = async (): Promise<Response> => {
    const url = new URL(`${BASE}/api/v1${path}`, typeof window === "undefined" ? "http://localhost" : window.location.origin);
    for (const [k, v] of Object.entries(opts.query ?? {})) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = {};
    const { accessToken } = useAuth.getState();
    if (accessToken) headers.Authorization = `Bearer ${accessToken}`;
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    return fetch(url.toString(), {
      method: opts.method ?? "GET",
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
  };

  let res = await attempt();
  if (res.status === 401 && !path.startsWith("/auth/")) {
    if (!refreshInFlight) {
      refreshInFlight = refreshSession().finally(() => {
        refreshInFlight = null;
      });
    }
    const refreshed = await refreshInFlight;
    if (refreshed) {
      res = await attempt();
    } else {
      useAuth.getState().clear();
      if (typeof window !== "undefined") window.location.href = "/login";
      throw new ApiError(401, "unauthorized", "session expired");
    }
  }

  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(
      res.status,
      data?.error?.code ?? "error",
      data?.error?.message ?? res.statusText,
    );
  }
  return data as T;
}

import { useAuth } from "@/stores/auth";

// wsUrl builds an authenticated WebSocket URL for an /api/v1 path. Browsers
// cannot set headers on WS upgrades, so the short-lived access token rides
// in the query string (the backend accepts it only for upgrade requests).
export function wsUrl(
  path: string,
  params: Record<string, string | number | boolean | undefined> = {},
): string {
  const base = (process.env.NEXT_PUBLIC_API_URL ?? "") || window.location.origin;
  const u = new URL(`/api/v1${path}`, base);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") u.searchParams.set(k, String(v));
  }
  const { accessToken } = useAuth.getState();
  if (accessToken) u.searchParams.set("access_token", accessToken);
  return u.toString();
}

export function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToB64(bytes: Uint8Array): string {
  let bin = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

export function textToB64(text: string): string {
  return bytesToB64(new TextEncoder().encode(text));
}

export function b64ToText(b64: string): string {
  return new TextDecoder().decode(b64ToBytes(b64));
}

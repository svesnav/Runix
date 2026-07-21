// Page URLs live here because their shape is constrained by how the UI is
// shipped: the control plane is a single binary that serves the app as
// exported static files, and Next cannot statically export a route whose
// path segments are only known at runtime (a server or runtime id). So
// those identifiers travel in the query string, where the client reads
// them, and every route on disk is a fixed file the binary can serve.

function withQuery(path: string, params: Record<string, string | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, v);
  }
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}

export function serverPath(
  id: string,
  params: { tab?: string; path?: string } = {},
): string {
  return withQuery("/servers/detail", { id, ...params });
}

export function runtimePath(serverId: string, type: string, rid: string): string {
  return withQuery("/runtimes", { server: serverId, type, rid });
}

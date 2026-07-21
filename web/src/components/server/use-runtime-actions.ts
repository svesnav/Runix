"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";

export interface RuntimeRef {
  type: string;
  id: string;
}

// useRuntimeActions centralizes runtime mutations (lifecycle actions, remove)
// so the type tables and the detail page behave identically and invalidate
// the same caches.
export function useRuntimeActions(serverId: string, onDone?: () => void) {
  const queryClient = useQueryClient();
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["runtimes", serverId] });
    onDone?.();
  };

  const action = useMutation({
    mutationFn: (input: RuntimeRef & { action: string; signal?: string }) =>
      api(`/servers/${serverId}/runtimes/${input.type}/${encodeURIComponent(input.id)}/actions`, {
        method: "POST",
        body: { action: input.action, signal: input.signal },
      }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "action failed"),
  });

  const remove = useMutation({
    mutationFn: (input: RuntimeRef & { purge?: boolean }) =>
      api(`/servers/${serverId}/runtimes/${input.type}/${encodeURIComponent(input.id)}`, {
        method: "DELETE",
        query: { force: true, purge: input.purge ?? false },
      }),
    onSuccess: invalidate,
    onError: (err) => alert(err instanceof ApiError ? err.message : "remove failed"),
  });

  return { action, remove };
}

// runtimeState helpers shared across views.
export function isRunning(state: string): boolean {
  return ["running", "degraded", "paused", "starting"].includes(state);
}

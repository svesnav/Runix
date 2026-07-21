"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dynamic from "next/dynamic";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive, ClipboardPaste, Copy, Download, File, FileArchive, FilePlus, Folder,
  FolderPlus, Lock, Pencil, Plus, Scissors, Trash2, Upload, X,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { downloadPaths, uploadFiles } from "@/lib/transfer";
import { b64ToText, textToB64 } from "@/lib/ws";
import { formatBytes, formatDate } from "@/lib/format";
import type { FSEntry, FSListResult, FSReadResult } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { ContextMenu, MenuItem } from "@/components/ui/context-menu";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, Select } from "@/components/ui/input";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { PermissionsDialog } from "@/components/server/permissions-dialog";

const MonacoEditor = dynamic(() => import("@monaco-editor/react"), { ssr: false });

const editableLimit = 1 << 20; // 1 MiB in the editor

interface Tab {
  id: number;
  path: string;
}

// Clipboard holds a pending copy/cut. A cut is applied as rename (move) on
// paste; a copy uses the server-side recursive copy.
interface Clipboard {
  op: "copy" | "cut";
  paths: string[];
}

export function FilesTab({
  serverId,
  online,
  initialPath = "/",
}: {
  serverId: string;
  online: boolean;
  initialPath?: string;
}) {
  const [tabs, setTabs] = useState<Tab[]>([{ id: 1, path: initialPath }]);
  const [activeId, setActiveId] = useState(1);
  const nextId = useRef(2);
  const [clipboard, setClipboard] = useState<Clipboard | null>(null);

  // A deep link (?path=) retargets the active tab rather than piling up tabs.
  useEffect(() => {
    setTabs((prev) => prev.map((t) => (t.id === activeId ? { ...t, path: initialPath } : t)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialPath]);

  const active = tabs.find((t) => t.id === activeId) ?? tabs[0];

  const setPath = (path: string) =>
    setTabs((prev) => prev.map((t) => (t.id === active.id ? { ...t, path } : t)));

  const addTab = (path: string) => {
    const id = nextId.current++;
    setTabs((prev) => [...prev, { id, path }]);
    setActiveId(id);
  };

  const closeTab = (id: number) => {
    setTabs((prev) => {
      const remaining = prev.filter((t) => t.id !== id);
      if (remaining.length === 0) return prev;
      if (id === activeId) setActiveId(remaining[remaining.length - 1].id);
      return remaining;
    });
  };

  if (!online) {
    return <p className="py-8 text-center text-sm text-ink-dim">Agent is offline — file access unavailable.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-1 border-b border-edge">
        {tabs.map((tab) => (
          <div
            key={tab.id}
            onClick={() => setActiveId(tab.id)}
            className={`group flex cursor-pointer items-center gap-2 border-b-2 px-3 py-1.5 text-xs ${
              tab.id === activeId
                ? "border-brand text-ink"
                : "border-transparent text-ink-dim hover:text-ink"
            }`}
          >
            <span className="max-w-40 truncate font-mono" title={tab.path}>
              {tab.path === "/" ? "/" : tab.path.split("/").pop()}
            </span>
            {tabs.length > 1 && (
              <button
                onClick={(e) => { e.stopPropagation(); closeTab(tab.id); }}
                className="cursor-pointer text-ink-dim opacity-0 hover:text-err group-hover:opacity-100"
                aria-label="Close tab"
              >
                <X size={11} />
              </button>
            )}
          </div>
        ))}
        <button
          onClick={() => addTab(active.path)}
          className="cursor-pointer px-2 py-1.5 text-ink-dim hover:text-ink"
          title="New tab"
        >
          <Plus size={13} />
        </button>
      </div>

      <FileBrowser
        key={active.id}
        serverId={serverId}
        path={active.path}
        setPath={setPath}
        openInNewTab={addTab}
        clipboard={clipboard}
        setClipboard={setClipboard}
      />
    </div>
  );
}

function FileBrowser({
  serverId,
  path,
  setPath,
  openInNewTab,
  clipboard,
  setClipboard,
}: {
  serverId: string;
  path: string;
  setPath: (p: string) => void;
  openInNewTab: (p: string) => void;
  clipboard: Clipboard | null;
  setClipboard: (c: Clipboard | null) => void;
}) {
  const queryClient = useQueryClient();
  const [hidden, setHidden] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ x: number; y: number; entry: FSEntry | null } | null>(null);
  const [editing, setEditing] = useState<{ path: string; text: string } | null>(null);
  const [permsFor, setPermsFor] = useState<FSEntry | null>(null);
  const [prompt, setPrompt] = useState<PromptState | null>(null);
  const [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);
  const [uploadPct, setUploadPct] = useState<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ["files", serverId] });
    setSelected(new Set());
  }, [queryClient, serverId]);

  const { data, error: listError } = useQuery({
    queryKey: ["files", serverId, path, hidden],
    queryFn: () => api<FSListResult>(`/servers/${serverId}/files`, { query: { path, hidden } }),
  });

  const entries = data?.entries ?? [];
  const selectedEntries = useMemo(
    () => entries.filter((e) => selected.has(e.path)),
    [entries, selected],
  );

  const run = async (fn: () => Promise<unknown>, label: string) => {
    setError("");
    try {
      await fn();
      invalidate();
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? `${label}: ${err.message}` : label);
    }
  };

  const join = (name: string) => (path === "/" ? `/${name}` : `${path}/${name}`);

  // Operations ---------------------------------------------------------------

  const doPaste = () => {
    if (!clipboard) return;
    void run(async () => {
      for (const src of clipboard.paths) {
        const name = src.split("/").pop()!;
        const dest = join(name);
        if (dest === src) continue;
        if (clipboard.op === "copy") {
          await api(`/servers/${serverId}/files/copy`, { method: "POST", body: { from: src, to: dest } });
        } else {
          await api(`/servers/${serverId}/files/rename`, { method: "POST", body: { from: src, to: dest } });
        }
      }
      if (clipboard.op === "cut") setClipboard(null);
    }, "paste");
  };

  const doDelete = (targets: FSEntry[]) => {
    if (targets.length === 0) return;
    const names = targets.map((t) => t.name).join(", ");
    if (!confirm(`Delete ${targets.length === 1 ? names : `${targets.length} items`}?`)) return;
    void run(async () => {
      for (const t of targets) {
        await api(`/servers/${serverId}/files`, {
          method: "DELETE",
          query: { path: t.path, recursive: t.isDir },
        });
      }
    }, "delete");
  };

  const doDownload = (targets: FSEntry[]) => {
    if (targets.length === 0) return;
    void run(
      () => downloadPaths(serverId, targets.map((t) => t.path), {
        archive: targets.length > 1 || targets[0].isDir,
      }),
      "download",
    );
  };

  const doUpload = async (files: File[]) => {
    if (files.length === 0) return;
    setError("");
    setUploadPct(0);
    try {
      const results = await uploadFiles(serverId, path, files, (p) =>
        setUploadPct(Math.round((p.loaded / p.total) * 100)),
      );
      const failed = results.filter((r) => r.error);
      if (failed.length) {
        setError(`upload: ${failed.map((f) => `${f.name} (${f.error})`).join("; ")}`);
      }
      invalidate();
    } catch (err) {
      setError(err instanceof Error ? `upload: ${err.message}` : "upload failed");
    } finally {
      setUploadPct(null);
    }
  };

  const openFile = async (entry: FSEntry) => {
    setError("");
    if (entry.size > editableLimit) {
      setError(`${entry.name} is ${formatBytes(entry.size)}; the editor opens files up to ${formatBytes(editableLimit)} — download it instead`);
      return;
    }
    try {
      const res = await api<FSReadResult>(`/servers/${serverId}/files/content`, {
        query: { path: entry.path, maxBytes: editableLimit },
      });
      setEditing({ path: entry.path, text: res.content ? b64ToText(res.content) : "" });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "read failed");
    }
  };

  const save = useMutation({
    mutationFn: () =>
      api(`/servers/${serverId}/files/content`, {
        method: "PUT",
        body: { path: editing!.path, content: textToB64(editing!.text) },
      }),
    onSuccess: () => setEditing(null),
    onError: (err) => setError(err instanceof ApiError ? err.message : "save failed"),
  });

  // Context menu -------------------------------------------------------------

  const menuItems = (entry: FSEntry | null): MenuItem[] => {
    // Right-clicking an unselected row acts on that row alone.
    const targets = entry
      ? selected.has(entry.path) && selectedEntries.length > 0
        ? selectedEntries
        : [entry]
      : selectedEntries;
    const single = targets.length === 1 ? targets[0] : null;

    const items: MenuItem[] = [];
    if (entry) {
      if (single?.isDir) {
        items.push({ label: "Open", icon: <Folder size={13} />, onSelect: () => setPath(single.path) });
        items.push({ label: "Open in new tab", icon: <Plus size={13} />, onSelect: () => openInNewTab(single.path) });
      } else if (single) {
        items.push({ label: "Edit", icon: <Pencil size={13} />, onSelect: () => void openFile(single) });
      }
      items.push({
        label: `Download${targets.length > 1 ? ` (${targets.length})` : ""}`,
        icon: <Download size={13} />,
        onSelect: () => doDownload(targets),
        separatorBefore: true,
      });
      items.push({
        label: "Copy", icon: <Copy size={13} />,
        onSelect: () => setClipboard({ op: "copy", paths: targets.map((t) => t.path) }),
        separatorBefore: true,
      });
      items.push({
        label: "Cut", icon: <Scissors size={13} />,
        onSelect: () => setClipboard({ op: "cut", paths: targets.map((t) => t.path) }),
      });
    }

    items.push({
      label: clipboard ? `Paste (${clipboard.paths.length})` : "Paste",
      icon: <ClipboardPaste size={13} />,
      onSelect: doPaste,
      disabled: !clipboard,
      separatorBefore: !entry,
    });

    if (entry && single) {
      items.push({
        label: "Rename", icon: <Pencil size={13} />, separatorBefore: true,
        onSelect: () => setPrompt({ kind: "rename", title: `Rename ${single.name}`, label: "New name", value: single.name, target: single }),
      });
      items.push({
        label: "Permissions", icon: <Lock size={13} />,
        onSelect: () => setPermsFor(single),
      });
    }

    if (targets.length > 0) {
      items.push({
        label: `Compress${targets.length > 1 ? ` (${targets.length})` : ""}`,
        icon: <Archive size={13} />, separatorBefore: true,
        onSelect: () => setPrompt({
          kind: "archive",
          title: `Compress ${targets.length === 1 ? targets[0].name : `${targets.length} items`}`,
          label: "Archive name",
          value: (targets.length === 1 ? targets[0].name : "archive") + ".tar.gz",
          targets,
          format: "tar.gz",
        }),
      });
      if (single && isArchive(single.name)) {
        items.push({
          label: "Extract here", icon: <FileArchive size={13} />,
          onSelect: () => void run(
            () => api(`/servers/${serverId}/files/extract`, { method: "POST", body: { path: single.path, dest: path } }),
            "extract",
          ),
        });
      }
    }

    items.push({ label: "New file", icon: <FilePlus size={13} />, separatorBefore: true,
      onSelect: () => setPrompt({ kind: "newFile", title: "New file", label: "File name", value: "" }) });
    items.push({ label: "New folder", icon: <FolderPlus size={13} />,
      onSelect: () => setPrompt({ kind: "newFolder", title: "New folder", label: "Folder name", value: "" }) });

    if (targets.length > 0) {
      items.push({
        label: `Delete${targets.length > 1 ? ` (${targets.length})` : ""}`,
        icon: <Trash2 size={13} />, danger: true, separatorBefore: true,
        onSelect: () => doDelete(targets),
      });
    }
    return items;
  };

  // Rendering ----------------------------------------------------------------

  const crumbs = pathCrumbs(path);
  const allSelected = entries.length > 0 && selected.size === entries.length;

  return (
    <div
      className="space-y-3"
      onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
      onDragLeave={(e) => {
        if (e.currentTarget === e.target) setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        const files = Array.from(e.dataTransfer.files);
        if (files.length) void doUpload(files);
      }}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <nav className="flex flex-wrap items-center gap-1 font-mono text-sm">
          {crumbs.map((c, i) => (
            <span key={c.path} className="flex items-center gap-1">
              {i > 0 && <span className="text-ink-dim">/</span>}
              <button className="cursor-pointer text-brand hover:underline" onClick={() => setPath(c.path)}>
                {c.label}
              </button>
            </span>
          ))}
        </nav>
        <label className="flex items-center gap-1.5 text-xs text-ink-dim">
          <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} className="accent-brand" />
          Hidden files
        </label>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <Button size="sm" variant="outline" onClick={() => setPrompt({ kind: "newFile", title: "New file", label: "File name", value: "" })}>
          <FilePlus size={13} /> New file
        </Button>
        <Button size="sm" variant="outline" onClick={() => setPrompt({ kind: "newFolder", title: "New folder", label: "Folder name", value: "" })}>
          <FolderPlus size={13} /> New folder
        </Button>
        <Button size="sm" variant="outline" onClick={() => fileInput.current?.click()}>
          <Upload size={13} /> Upload
        </Button>
        <span className="mx-1 h-5 w-px bg-edge" />
        <Button size="sm" variant="outline" disabled={selectedEntries.length === 0}
          onClick={() => doDownload(selectedEntries)}>
          <Download size={13} /> Download
        </Button>
        <Button size="sm" variant="outline" disabled={selectedEntries.length === 0}
          onClick={() => setClipboard({ op: "copy", paths: selectedEntries.map((e) => e.path) })}>
          <Copy size={13} /> Copy
        </Button>
        <Button size="sm" variant="outline" disabled={selectedEntries.length === 0}
          onClick={() => setClipboard({ op: "cut", paths: selectedEntries.map((e) => e.path) })}>
          <Scissors size={13} /> Cut
        </Button>
        <Button size="sm" variant="outline" disabled={!clipboard} onClick={doPaste}>
          <ClipboardPaste size={13} /> Paste{clipboard ? ` (${clipboard.paths.length})` : ""}
        </Button>
        <span className="mx-1 h-5 w-px bg-edge" />
        <Button size="sm" variant="outline" disabled={selectedEntries.length === 0}
          onClick={() => setPrompt({
            kind: "archive",
            title: `Compress ${selectedEntries.length} item${selectedEntries.length === 1 ? "" : "s"}`,
            label: "Archive name",
            value: (selectedEntries.length === 1 ? selectedEntries[0].name : "archive") + ".tar.gz",
            targets: selectedEntries,
            format: "tar.gz",
          })}>
          <Archive size={13} /> Compress
        </Button>
        <Button size="sm" variant="outline"
          disabled={selectedEntries.length !== 1 || !isArchive(selectedEntries[0].name)}
          onClick={() => void run(
            () => api(`/servers/${serverId}/files/extract`, { method: "POST", body: { path: selectedEntries[0].path, dest: path } }),
            "extract",
          )}>
          <FileArchive size={13} /> Extract
        </Button>
        <Button size="sm" variant="outline" disabled={selectedEntries.length !== 1}
          onClick={() => setPermsFor(selectedEntries[0])}>
          <Lock size={13} /> Permissions
        </Button>
        <Button size="sm" variant="danger" disabled={selectedEntries.length === 0}
          onClick={() => doDelete(selectedEntries)}>
          <Trash2 size={13} /> Delete
        </Button>
      </div>

      <input
        ref={fileInput}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          e.target.value = "";
          void doUpload(files);
        }}
      />

      {uploadPct !== null && (
        <div className="flex items-center gap-2 text-xs text-ink-dim">
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-card">
            <div className="h-full bg-brand transition-all" style={{ width: `${uploadPct}%` }} />
          </div>
          uploading {uploadPct}%
        </div>
      )}

      {(error || listError) && (
        <p className="text-xs text-err">
          {error || (listError instanceof ApiError ? listError.message : "listing failed")}
        </p>
      )}

      <Card className={dragging ? "border-brand ring-1 ring-brand/40" : undefined}>
        <div onContextMenu={(e) => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY, entry: null }); }}>
          <Table>
            <THead>
              <TR>
                <TH className="w-8">
                  <input
                    type="checkbox"
                    className="accent-brand"
                    checked={allSelected}
                    onChange={(e) =>
                      setSelected(e.target.checked ? new Set(entries.map((x) => x.path)) : new Set())
                    }
                  />
                </TH>
                <TH>Name</TH><TH>Size</TH><TH>Mode</TH><TH>Modified</TH>
              </TR>
            </THead>
            <TBody>
              {path !== "/" && (
                <TR className="cursor-pointer hover:bg-card/60" onClick={() => setPath(parentPath(path))}>
                  <TD colSpan={5} className="font-mono text-xs text-ink-dim">..</TD>
                </TR>
              )}
              {entries.map((entry) => (
                <TR
                  key={entry.path}
                  className={selected.has(entry.path) ? "bg-brand/10" : "hover:bg-card/40"}
                  onContextMenu={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    setMenu({ x: e.clientX, y: e.clientY, entry });
                  }}
                >
                  <TD onClick={(e) => e.stopPropagation()}>
                    <input
                      type="checkbox"
                      className="accent-brand"
                      checked={selected.has(entry.path)}
                      onChange={(e) => {
                        const next = new Set(selected);
                        if (e.target.checked) next.add(entry.path);
                        else next.delete(entry.path);
                        setSelected(next);
                      }}
                    />
                  </TD>
                  <TD>
                    <button
                      className="flex cursor-pointer items-center gap-2 text-left font-mono text-xs hover:text-brand"
                      onClick={() => (entry.isDir ? setPath(entry.path) : void openFile(entry))}
                    >
                      {entry.isDir
                        ? <Folder size={14} className="shrink-0 text-brand" />
                        : <File size={14} className="shrink-0 text-ink-dim" />}
                      {entry.name}
                      {entry.isSymlink && <span className="text-ink-dim">→</span>}
                    </button>
                  </TD>
                  <TD className="text-xs text-ink-dim">{entry.isDir ? "—" : formatBytes(entry.size)}</TD>
                  <TD className="font-mono text-xs text-ink-dim">{entry.mode}</TD>
                  <TD className="text-xs text-ink-dim">{formatDate(entry.modTime)}</TD>
                </TR>
              ))}
              {data && entries.length === 0 && (
                <TR><TD colSpan={5} className="py-6 text-center text-ink-dim">
                  Empty directory — drop files here to upload
                </TD></TR>
              )}
            </TBody>
          </Table>
        </div>
      </Card>

      {selectedEntries.length > 0 && (
        <p className="text-xs text-ink-dim">{selectedEntries.length} selected</p>
      )}

      {menu && (
        <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.entry)} onClose={() => setMenu(null)} />
      )}

      {permsFor && (
        <PermissionsDialog serverId={serverId} entry={permsFor} onClose={() => setPermsFor(null)} onDone={invalidate} />
      )}

      {prompt && (
        <PromptDialog
          state={prompt}
          onClose={() => setPrompt(null)}
          onSubmit={(value, format) => {
            const p = prompt;
            setPrompt(null);
            switch (p.kind) {
              case "newFile":
                return run(() => api(`/servers/${serverId}/files/create`, { method: "POST", body: { path: join(value) } }), "create file");
              case "newFolder":
                return run(() => api(`/servers/${serverId}/files/mkdir`, { method: "POST", body: { path: join(value) } }), "create folder");
              case "rename":
                return run(() => api(`/servers/${serverId}/files/rename`, {
                  method: "POST",
                  body: { from: p.target!.path, to: join(value) },
                }), "rename");
              case "archive":
                return run(() => api(`/servers/${serverId}/files/archive`, {
                  method: "POST",
                  body: { paths: p.targets!.map((t) => t.path), target: join(value), format },
                }), "compress");
            }
          }}
        />
      )}

      <Dialog open={editing !== null} onClose={() => setEditing(null)} title={editing?.path ?? ""} wide>
        {editing && (
          <div className="space-y-3">
            <div className="overflow-hidden rounded-md border border-edge">
              <MonacoEditor
                height="55vh"
                theme="vs-dark"
                path={editing.path}
                value={editing.text}
                onChange={(v) => setEditing((prev) => (prev ? { ...prev, text: v ?? "" } : prev))}
                options={{ fontSize: 12, minimap: { enabled: false }, scrollBeyondLastLine: false }}
              />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setEditing(null)}>Close</Button>
              <Button onClick={() => save.mutate()} disabled={save.isPending}>Save</Button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  );
}

// Prompt dialog ---------------------------------------------------------------

interface PromptState {
  kind: "newFile" | "newFolder" | "rename" | "archive";
  title: string;
  label: string;
  value: string;
  target?: FSEntry;
  targets?: FSEntry[];
  format?: string;
}

function PromptDialog({
  state,
  onClose,
  onSubmit,
}: {
  state: PromptState;
  onClose: () => void;
  onSubmit: (value: string, format?: string) => void;
}) {
  const [value, setValue] = useState(state.value);
  const [format, setFormat] = useState(state.format ?? "tar.gz");

  // Keep the extension in sync when the archive format changes.
  const changeFormat = (next: string) => {
    setFormat(next);
    setValue((v) => v.replace(/\.(tar\.gz|tgz|zip)$/i, "") + (next === "zip" ? ".zip" : ".tar.gz"));
  };

  return (
    <Dialog open onClose={onClose} title={state.title}>
      <form
        onSubmit={(e) => { e.preventDefault(); if (value.trim()) onSubmit(value.trim(), format); }}
        className="space-y-4"
      >
        <Field label={state.label}>
          <Input autoFocus value={value} onChange={(e) => setValue(e.target.value)} className="font-mono" />
        </Field>
        {state.kind === "archive" && (
          <Field label="Format">
            <Select value={format} onChange={(e) => changeFormat(e.target.value)}>
              <option value="tar.gz">tar.gz (gzip)</option>
              <option value="zip">zip</option>
            </Select>
          </Field>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
          <Button type="submit" disabled={!value.trim()}>Confirm</Button>
        </div>
      </form>
    </Dialog>
  );
}

// Helpers ---------------------------------------------------------------------

function isArchive(name: string): boolean {
  return /\.(tar\.gz|tgz|tar|zip)$/i.test(name);
}

function pathCrumbs(path: string): { label: string; path: string }[] {
  const crumbs = [{ label: "/", path: "/" }];
  let acc = "";
  for (const part of path.split("/").filter(Boolean)) {
    acc += "/" + part;
    crumbs.push({ label: part, path: acc });
  }
  return crumbs;
}

function parentPath(path: string): string {
  const idx = path.lastIndexOf("/");
  return idx <= 0 ? "/" : path.slice(0, idx);
}

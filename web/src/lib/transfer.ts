import { apiURL, authFetch } from "@/lib/api";
import { useAuth } from "@/stores/auth";

// downloadPaths fetches one or more paths as a blob and hands it to the
// browser's download machinery. A plain anchor cannot be used because the
// endpoint needs an Authorization header.
export async function downloadPaths(
  serverId: string,
  paths: string[],
  opts: { archive?: boolean } = {},
): Promise<void> {
  const url = apiURL(`/servers/${serverId}/files/download`, {
    path: paths,
    archive: opts.archive ? "true" : undefined,
  });
  const res = await authFetch(url);
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw new Error(body?.error?.message ?? `download failed (${res.status})`);
  }
  const blob = await res.blob();
  const name = filenameFromDisposition(res.headers.get("Content-Disposition")) ?? guessName(paths, opts.archive);

  const objectUrl = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = objectUrl;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Revoke on the next tick so the click has consumed the URL.
  setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
}

function guessName(paths: string[], archive?: boolean): string {
  if (paths.length !== 1 || archive) return "download.tar.gz";
  return paths[0].split("/").pop() || "download";
}

function filenameFromDisposition(value: string | null): string | null {
  if (!value) return null;
  const match = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(value);
  return match ? decodeURIComponent(match[1]) : null;
}

export interface UploadProgress {
  loaded: number;
  total: number;
}

export interface UploadedFile {
  name: string;
  path: string;
  size: number;
  error?: string;
}

// uploadFiles posts files as multipart/form-data via XHR, which (unlike
// fetch) reports upload progress. All selected files go in one request so
// the agent writes them back-to-back.
export function uploadFiles(
  serverId: string,
  dir: string,
  files: File[],
  onProgress?: (p: UploadProgress) => void,
): Promise<UploadedFile[]> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    for (const file of files) form.append("files", file, file.name);

    const xhr = new XMLHttpRequest();
    xhr.open("POST", apiURL(`/servers/${serverId}/files/upload`, { path: dir }));
    const { accessToken } = useAuth.getState();
    if (accessToken) xhr.setRequestHeader("Authorization", `Bearer ${accessToken}`);

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.({ loaded: e.loaded, total: e.total });
    };
    xhr.onload = () => {
      let body: { files?: UploadedFile[]; error?: { message?: string } } | null = null;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        body = null;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body?.files ?? []);
      } else {
        reject(new Error(body?.error?.message ?? `upload failed (${xhr.status})`));
      }
    };
    xhr.onerror = () => reject(new Error("upload failed: network error"));
    xhr.send(form);
  });
}

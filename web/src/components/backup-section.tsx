"use client";

import { useRef, useState } from "react";
import { Download, Upload } from "lucide-react";
import { apiURL, authFetch } from "@/lib/api";
import { useT } from "@/i18n";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";

interface ImportReport {
  created: Record<string, number>;
  skipped: Record<string, number>;
  errors?: string[];
}

export function BackupSection() {
  const t = useT();
  const fileInput = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [error, setError] = useState("");

  const exportBackup = async () => {
    setError("");
    setBusy(true);
    try {
      const res = await authFetch(apiURL("/backup/export"));
      if (!res.ok) throw new Error(`export failed (${res.status})`);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `runix-backup-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "export failed");
    } finally {
      setBusy(false);
    }
  };

  const importBackup = async (file: File) => {
    setError("");
    setReport(null);
    setBusy(true);
    try {
      const body = await file.text();
      const res = await authFetch(apiURL("/backup/import"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error?.message ?? `import failed (${res.status})`);
      setReport(data as ImportReport);
    } catch (err) {
      setError(err instanceof Error ? err.message : "import failed");
    } finally {
      setBusy(false);
    }
  };

  const summarize = (counts: Record<string, number>) =>
    Object.entries(counts)
      .filter(([, n]) => n > 0)
      .map(([kind, n]) => `${kind}: ${n}`)
      .join(", ") || "—";

  return (
    <Card>
      <CardHeader><CardTitle>{t.settings.backupTitle}</CardTitle></CardHeader>
      <CardBody className="space-y-3">
        <p className="text-xs text-ink-dim">{t.settings.backupIntro}</p>
        <div className="flex gap-2">
          <Button variant="outline" onClick={exportBackup} disabled={busy}>
            <Download size={13} /> {t.settings.exportBackup}
          </Button>
          <Button variant="outline" onClick={() => fileInput.current?.click()} disabled={busy}>
            <Upload size={13} /> {t.settings.importBackup}
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              if (file) void importBackup(file);
            }}
          />
        </div>

        {error && <p className="text-xs text-err">{error}</p>}
        {report && (
          <div className="rounded-md border border-edge bg-canvas p-3 text-xs">
            <div className="font-medium text-ink">{t.settings.importResult}</div>
            <div className="mt-1 text-ink-dim">
              {t.settings.importCreated}: {summarize(report.created)}
            </div>
            <div className="text-ink-dim">
              {t.settings.importSkipped}: {summarize(report.skipped)}
            </div>
            {report.errors?.map((message, i) => (
              <div key={i} className="mt-1 text-warn">{message}</div>
            ))}
          </div>
        )}
      </CardBody>
    </Card>
  );
}

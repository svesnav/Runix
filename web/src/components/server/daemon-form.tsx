"use client";

import { useState } from "react";
import { Plus, X } from "lucide-react";
import type { DaemonConfig } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Field, Input, Label, Select } from "@/components/ui/input";

// EnvPair is one editable environment variable row.
interface EnvPair {
  key: string;
  value: string;
}

export interface DaemonFormValue {
  name: string;
  command: string;
  args: string;
  workingDir: string;
  env: EnvPair[];
  autoStart: boolean;
  restartPolicy: "never" | "on-failure" | "always";
  maxRestarts: number;
  restartDelaySeconds: number;
  stopSignal: string;
  stopTimeoutSeconds: number;
}

export function emptyDaemonForm(): DaemonFormValue {
  return {
    name: "",
    command: "",
    args: "",
    workingDir: "",
    env: [],
    autoStart: true,
    restartPolicy: "on-failure",
    maxRestarts: 0,
    restartDelaySeconds: 2,
    stopSignal: "SIGTERM",
    stopTimeoutSeconds: 10,
  };
}

// daemonFormFromConfig builds the form value from an existing daemon spec
// (edit mode). cmd[0] is the command, the rest are args.
export function daemonFormFromConfig(name: string, config: DaemonConfig): DaemonFormValue {
  const cmd = config.cmd ?? [];
  return {
    name,
    command: cmd[0] ?? "",
    args: quoteArgs(cmd.slice(1)),
    workingDir: config.workingDir ?? "",
    env: Object.entries(config.env ?? {}).map(([key, value]) => ({ key, value })),
    autoStart: config.autoStart ?? false,
    restartPolicy: config.restartPolicy ?? "on-failure",
    maxRestarts: config.maxRestarts ?? 0,
    restartDelaySeconds: config.restartDelaySeconds ?? 2,
    stopSignal: config.stopSignal ?? "SIGTERM",
    stopTimeoutSeconds: config.stopTimeoutSeconds ?? 10,
  };
}

// toConfig converts the form to the agent's daemon config document. Args are
// split on whitespace, honoring simple single/double quoting so paths with
// spaces survive.
export function daemonFormToConfig(v: DaemonFormValue): DaemonConfig {
  const env: Record<string, string> = {};
  for (const pair of v.env) {
    if (pair.key.trim()) env[pair.key.trim()] = pair.value;
  }
  return {
    cmd: [v.command.trim(), ...tokenizeArgs(v.args)],
    workingDir: v.workingDir.trim() || undefined,
    env: Object.keys(env).length ? env : undefined,
    autoStart: v.autoStart,
    restartPolicy: v.restartPolicy,
    maxRestarts: v.maxRestarts,
    restartDelaySeconds: v.restartDelaySeconds,
    stopSignal: v.stopSignal,
    stopTimeoutSeconds: v.stopTimeoutSeconds,
  };
}

function tokenizeArgs(input: string): string[] {
  const out: string[] = [];
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(input)) !== null) {
    out.push(m[1] ?? m[2] ?? m[3] ?? "");
  }
  return out;
}

// quoteArgs is the inverse of tokenizeArgs: it re-quotes any token that
// contains whitespace or quotes so the args string round-trips through the
// tokenizer without a token with spaces being split into several.
function quoteArgs(tokens: string[]): string {
  return tokens
    .map((t) => {
      if (t === "" ) return '""';
      if (!/[\s"']/.test(t)) return t;
      if (!t.includes('"')) return `"${t}"`;
      return `'${t}'`;
    })
    .join(" ");
}

export function DaemonForm({
  value,
  onChange,
  editing,
}: {
  value: DaemonFormValue;
  onChange: (v: DaemonFormValue) => void;
  editing: boolean;
}) {
  const set = <K extends keyof DaemonFormValue>(key: K, v: DaemonFormValue[K]) =>
    onChange({ ...value, [key]: v });

  const [envDraft, setEnvDraft] = useState<EnvPair>({ key: "", value: "" });

  const addEnv = () => {
    if (!envDraft.key.trim()) return;
    set("env", [...value.env, { ...envDraft, key: envDraft.key.trim() }]);
    setEnvDraft({ key: "", value: "" });
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-4">
        <Field label="Name">
          <Input
            value={value.name}
            onChange={(e) => set("name", e.target.value)}
            disabled={editing}
            placeholder="my-service"
          />
        </Field>
        <Field label="Working directory (optional)">
          <Input
            value={value.workingDir}
            onChange={(e) => set("workingDir", e.target.value)}
            placeholder="/opt/my-service"
          />
        </Field>
      </div>

      <Field label="Command (executable path)">
        <Input
          value={value.command}
          onChange={(e) => set("command", e.target.value)}
          placeholder="/usr/local/bin/my-service"
          className="font-mono"
        />
      </Field>
      <Field label="Arguments">
        <Input
          value={value.args}
          onChange={(e) => set("args", e.target.value)}
          placeholder="--port 9000 --config /etc/app.yaml"
          className="font-mono"
        />
      </Field>

      <div>
        <Label>Environment variables</Label>
        <div className="space-y-1.5">
          {value.env.map((pair, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                className="font-mono"
                value={pair.key}
                onChange={(e) => {
                  const env = [...value.env];
                  env[i] = { ...env[i], key: e.target.value };
                  set("env", env);
                }}
              />
              <span className="text-ink-dim">=</span>
              <Input
                className="font-mono"
                value={pair.value}
                onChange={(e) => {
                  const env = [...value.env];
                  env[i] = { ...env[i], value: e.target.value };
                  set("env", env);
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => set("env", value.env.filter((_, j) => j !== i))}
              >
                <X size={13} />
              </Button>
            </div>
          ))}
          <div className="flex items-center gap-2">
            <Input
              className="font-mono"
              placeholder="KEY"
              value={envDraft.key}
              onChange={(e) => setEnvDraft({ ...envDraft, key: e.target.value })}
            />
            <span className="text-ink-dim">=</span>
            <Input
              className="font-mono"
              placeholder="value"
              value={envDraft.value}
              onChange={(e) => setEnvDraft({ ...envDraft, value: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addEnv();
                }
              }}
            />
            <Button type="button" variant="outline" size="sm" onClick={addEnv}>
              <Plus size={13} />
            </Button>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Field label="Restart policy">
          <Select value={value.restartPolicy} onChange={(e) => set("restartPolicy", e.target.value as DaemonFormValue["restartPolicy"])}>
            <option value="never">Never</option>
            <option value="on-failure">On failure (non-zero exit)</option>
            <option value="always">Always</option>
          </Select>
        </Field>
        <Field label="Max restarts (0 = unlimited)">
          <Input
            type="number"
            min={0}
            value={value.maxRestarts}
            onChange={(e) => set("maxRestarts", Number(e.target.value))}
          />
        </Field>
        <Field label="Restart delay (seconds)">
          <Input
            type="number"
            min={1}
            value={value.restartDelaySeconds}
            onChange={(e) => set("restartDelaySeconds", Number(e.target.value))}
          />
        </Field>
        <Field label="Stop timeout (seconds)">
          <Input
            type="number"
            min={1}
            value={value.stopTimeoutSeconds}
            onChange={(e) => set("stopTimeoutSeconds", Number(e.target.value))}
          />
        </Field>
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="accent-brand"
          checked={value.autoStart}
          onChange={(e) => set("autoStart", e.target.checked)}
        />
        Start automatically (on creation and when the agent boots)
      </label>
    </div>
  );
}

// API types mirroring the Go DTOs (api/openapi.yaml is authoritative).

export interface User {
  id: string;
  username: string;
  email: string;
  displayName: string;
  isActive: boolean;
  mustChangePassword: boolean;
  totpEnabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface TokenPair {
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
}

export interface LoginResponse {
  mfaRequired: boolean;
  mfaToken?: string;
  tokens?: TokenPair;
  user?: User;
}

export interface MeResponse {
  user: User;
  permissions: string[];
  roles: string[];
}

export interface ListResponse<T> {
  items: T[];
  total: number;
  page: number;
  size: number;
}

export interface Server {
  id: string;
  name: string;
  description: string;
  address: string;
  hostname: string;
  os: string;
  osVersion: string;
  kernelVersion: string;
  architecture: string;
  agentVersion: string;
  location: string;
  tags: string[];
  labels: Record<string, string>;
  cpuCores: number;
  memoryBytes: number;
  swapBytes: number;
  diskBytes: number;
  dockerAvailable: boolean;
  systemdAvailable: boolean;
  runtimeTypes: string[];
  connectionStatus: "never_connected" | "online" | "offline";
  lastHeartbeatAt?: string;
  lastSeenAt?: string;
  createdAt: string;
}

export interface ServerCreated {
  server: Server;
  agentToken: string;
}

export type RuntimeState =
  | "created" | "starting" | "running" | "degraded" | "paused"
  | "stopping" | "stopped" | "failed" | "unknown";

export interface RuntimeStatus {
  state: RuntimeState;
  health: "unknown" | "starting" | "healthy" | "unhealthy";
  message?: string;
  exitCode?: number;
  startedAt?: string;
  finishedAt?: string;
  restartCount: number;
}

export interface RuntimeDescriptor {
  id: string;
  type: string;
  name: string;
  labels?: Record<string, string>;
  createdAt: string;
  status: RuntimeStatus;
}

export interface RuntimeInfo {
  descriptor: RuntimeDescriptor;
  capabilities: string[];
}

// DaemonConfig is the form-friendly shape of a native daemon's config,
// matching the agent's daemon.Spec JSON.
export interface DaemonConfig {
  cmd: string[];
  workingDir?: string;
  env?: Record<string, string>;
  autoStart: boolean;
  restartPolicy: "never" | "on-failure" | "always";
  maxRestarts: number;
  restartDelaySeconds: number;
  maxRestartDelaySeconds?: number;
  stopSignal?: string;
  // Written to the process's console before any signal is sent.
  stopCommand?: string;
  stopTimeoutSeconds?: number;
}

export interface DaemonInspect {
  spec: DaemonConfig & { name: string };
  pid?: number;
  workingDir: string;
  status: RuntimeStatus;
}

export interface MetricsPoint {
  serverId: string;
  collectedAt: string;
  cpuPercent: number;
  load1: number;
  load5: number;
  load15: number;
  memoryUsed: number;
  memoryTotal: number;
  swapUsed: number;
  swapTotal: number;
  diskUsed: number;
  diskTotal: number;
  netRxBytes: number;
  netTxBytes: number;
  temperature?: number;
  uptimeSecs: number;
}

export interface DashboardSummary {
  servers: Record<string, number>;
  connectedAgents: number;
  runtimes: Record<string, Record<string, number>>;
  recentEvents: { topic: string; serverId?: string; at: string }[];
}

export interface Role {
  id: string;
  key: string;
  name: string;
  description: string;
  isSystem: boolean;
  permissions: string[];
}

export interface AuditEntry {
  id: number;
  time: string;
  actorId?: string;
  actorName?: string;
  ip?: string;
  action: string;
  targetType?: string;
  targetId?: string;
  result: "success" | "failure";
  error?: string;
}

export interface Setting {
  key: string;
  value: unknown;
  updatedAt: string;
}

// What the control plane says about a setting, so the UI can render the
// right control instead of asking for hand-written JSON.
export interface SettingDescriptor {
  key: string;
  label: string;
  description: string;
  group: string;
  kind: "string" | "int" | "bool";
  unit?: string;
  min?: number;
  max?: number;
  default?: unknown;
}

export interface Session {
  id: string;
  userAgent: string;
  ip: string;
  remember: boolean;
  createdAt: string;
  lastUsedAt: string;
  expiresAt: string;
}

export interface PAT {
  id: string;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
}

export interface CreatedPAT {
  token: PAT;
  plainToken: string;
}

export interface DockerImage {
  id: string;
  repoTags: string[];
  size: number;
  created: number;
  containers: number;
  dangling: boolean;
}

export interface DockerVolume {
  name: string;
  driver: string;
  mountpoint: string;
  createdAt: string;
  labels?: Record<string, string>;
}

export interface DockerNetwork {
  id: string;
  name: string;
  driver: string;
  scope: string;
  internal: boolean;
  containers: number;
}

export interface DockerDiskUsage {
  imagesSize: number;
  containersSize: number;
  volumesSize: number;
}

export interface ComposeInspect {
  project: string;
  composeFile?: string;
  content?: string;
  services: unknown[];
}

// PermissionDescriptor pairs the stored dotted key with a human-readable
// name, so the UI never has to show machine identifiers.
export interface PermissionDescriptor {
  key: string;
  label: string;
  description: string;
  group: string;
}

export interface ScheduledTask {
  id: string;
  name: string;
  description: string;
  serverId: string;
  kind: "runtime_action" | "runtime_exec";
  payload: {
    runtimeType: string;
    runtimeId: string;
    action?: string;
    cmd?: string[];
  };
  cron: string;
  enabled: boolean;
  nextRunAt?: string;
  lastRunAt?: string;
  lastStatus?: "success" | "failure" | "";
  lastError?: string;
}

export interface TaskRun {
  id: number;
  taskId: string;
  startedAt: string;
  durationMs: number;
  status: "success" | "failure";
  detail?: string;
}

export interface Grant {
  id: string;
  subjectType: "user" | "group" | "role";
  subjectId: string;
  permission: string;
  scopeType: "global" | "server_group" | "server" | "runtime";
  scopeId?: string;
  createdAt: string;
}

export interface Group {
  id: string;
  name: string;
  description: string;
}

// ServerGroup is a group of servers (distinct from Group, which groups
// users); permissions can be scoped to one.
export interface ServerGroup {
  id: string;
  name: string;
  description: string;
  createdAt: string;
}

export interface FSEntry {
  name: string;
  path: string;
  size: number;
  mode: string;
  modTime: string;
  isDir: boolean;
  isSymlink: boolean;
}

export interface FSListResult {
  path: string;
  entries: FSEntry[];
}

export interface FSReadResult {
  content: string; // base64
  size: number;
  truncated?: boolean;
}

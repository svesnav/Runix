# Runix Architecture

This document records the load-bearing design decisions of Runix and the
rationale behind them. It is updated whenever a decision changes.

## Goals

Runix manages thousands of servers and their workloads through one
consistent interface. Every decision is weighed against security,
reliability, scalability, maintainability and long-term evolution — in that
spirit, the platform is built as a modular monolith control plane plus a
fleet of thin agents, not a microservice constellation: one deployable unit
per role, strict internal boundaries.

## Topology

```
                ┌────────────────────────────┐
   Browser ────▶│  runix-server (control     │◀──── REST / OpenAPI clients
   (Next.js)    │  plane): Gin HTTP + WS,    │
                │  PostgreSQL, Redis          │
                └────────────▲───────────────┘
                             │ outbound WSS (agent dials out)
              ┌──────────────┼──────────────┐
              │              │              │
        ┌─────┴────┐   ┌─────┴────┐   ┌─────┴────┐
        │ runix-   │   │ runix-   │   │ runix-   │
        │ agent    │   │ agent    │   │ agent    │
        │ docker/  │   │ systemd/ │   │ daemon/  │
        │ compose  │   │ ...      │   │ ...      │
        └──────────┘   └──────────┘   └──────────┘
```

Key property: **agents dial out** to the control plane over WebSocket. The
control plane never opens connections into managed networks, which makes
NAT-ed and firewalled hosts first-class and keeps the attack surface on the
server side. Commands, log streams and terminals are multiplexed over the
agent's single authenticated connection.

## Layering

Clean Architecture, enforced by import direction (inner layers never import
outer ones):

| Layer | Location | Contains |
|---|---|---|
| Domain | `internal/domain/...` | Pure business model: the runtime abstraction, entities, state machines. No Gin, no SQL, no I/O. |
| Application | `internal/modules/*/service.go` | Use cases, orchestration, authorization decisions |
| Infrastructure | `internal/modules/*/repository.go`, `internal/platform/...` | PostgreSQL, Redis, OS integration |
| Transport | `internal/modules/*/handler.go`, `internal/server`, WS | HTTP/WS in, DTOs out. Zero business logic. |

### Module pattern

Features are vertical slices under `internal/modules/<name>/` with a
conventional internal layout:

```
internal/modules/<name>/
    handler.go       transport: parses/validates input, calls the service
    service.go       application logic, independent from HTTP
    repository.go    persistence behind an interface owned by the service
    model.go         module entities
    dto.go           request/response shapes (never domain types on the wire)
    routes.go        route registration: RegisterRoutes(r gin.IRouter, h *Handler)
    *_test.go
```

Modules never import each other's internals; cross-module needs go through
interfaces defined by the consumer. `internal/modules/health` is the
reference implementation of the pattern.

The shared kernel is deliberately tiny: `internal/platform/{config, logger,
version}` plus, later, database and redis clients. Anything feature-shaped
belongs in a module.

## The Runtime abstraction

The core product idea: Docker containers, Compose projects, systemd units,
native daemons and future workloads (Podman, Kubernetes, VMs) share one
lifecycle. This lives in `internal/domain/runtime` and nothing in it knows
about any concrete technology.

Design decisions, and why:

1. **Small core interface + optional capability interfaces**, instead of one
   fat interface with 17 methods. Not every runtime can `Pause` (systemd
   can't) or `Exec` (a plain process can't meaningfully). With a fat
   interface every provider stubs half the methods with "not supported"
   errors and the compiler verifies nothing. Instead:
   - `runtime.Runtime` carries only the universal contract: identity,
     `Status`, `Start`, `Stop`, `Restart`.
   - Optional operations are narrow interfaces (`Pauser`, `Reloader`,
     `Killer`, `LogStreamer`, `MetricsProvider`, `Execer`,
     `TerminalProvider`, `ConsoleProvider`, `HealthChecker`, `Inspector`) a
     provider opts into by implementing them.
   - `CapabilitiesOf(rt)` derives support via interface assertions, and
     `CapabilitySet` (a bitmask) travels over the wire so UIs render only
     the actions that exist. Callers never invoke methods that can only
     fail.

2. **`Provider` is the extension point.** A provider manages the population
   of one runtime type on one host (`List/Get/Create/Remove` + declared
   capabilities + availability). Adding Podman support means implementing
   `Provider` and registering it in the agent — nothing above the
   `Registry` changes. Availability is a report (`Availability{Available,
   Version, Message}`), not an error, so "Docker is not installed" is a
   fact the UI can show rather than a hole in the API.

3. **Provider-independent state machine.** Providers translate native
   states into `created / starting / running / degraded / paused /
   stopping / stopped / failed / unknown`. Transitions are whitelisted
   (`CanTransitionTo`), with `unknown` as an explicit wildcard because
   contact with any workload can be lost at any moment and reconciliation
   can discover anything afterwards. `Health` is deliberately separate from
   `State`: a running container can be unhealthy.

4. **Typed spec envelope.** `Spec.Config` is a `json.RawMessage` the
   provider unmarshals into its own typed config. The domain validates the
   universal part (name, type, labels); the provider validates its own
   document. This keeps the domain closed for modification and providers
   open for extension.

5. **Streaming as interfaces.** `LogStream` (pull-based, `io.EOF`
   semantics) and `Terminal` (`io.ReadWriteCloser` + `Resize`) are neutral
   contracts that both the WebSocket transport and tests can consume
   without knowing the backing technology.

## Agent design

The agent is intentionally thin: it executes, it does not decide. All
business rules (permissions, policies, scheduling of restarts across the
fleet) live in the control plane. The agent:

- registers the providers usable on its host into a `runtime.Registry`,
- reports availability, heartbeats and metrics,
- executes commands received over its outbound connection,
- streams logs/terminals.

It is a single static binary (`CGO_ENABLED=0`) so one artifact per
architecture serves every distro.

## Cross-platform policy

Runix targets **linux/amd64 and linux/arm64** as first-class deployment
platforms (development also happens on Windows/macOS hosts).

- `CGO_ENABLED=0` everywhere, no exceptions without an architecture
  decision recorded here. This keeps cross-compilation a pure `GOOS/GOARCH`
  switch and binaries fully static.
- OS-specific integrations (systemd, /proc metrics) are isolated behind
  provider interfaces with `//go:build linux` files; shared code stays
  portable and is tested on the host platform.
- CI builds both architectures on every change (`.github/workflows/ci.yml`);
  `make release` produces the dist matrix.

## Security model (design targets)

- AuthN: JWT access tokens (short-lived) + refresh tokens, TOTP MFA,
  personal access tokens for API use. Passwords hashed with argon2id.
- AuthZ: RBAC — users, groups, roles, permissions — evaluated on every
  request in the application layer (never in handlers), grantable globally,
  per group, per server, per runtime.
- Agents authenticate with per-agent tokens issued at enrollment; the
  channel is WSS.
- Every state-changing action produces an audit entry (who, from where,
  what, old/new value, result).
- Handlers accept DTOs only; validation happens at the transport boundary,
  authorization in services, parameterized SQL only in repositories.

## Persistence

- PostgreSQL is the system of record; schema evolves exclusively through
  versioned migrations in `migrations/` (golang-migrate convention).
- Redis carries ephemeral state: sessions, pub/sub fan-out for WS events,
  caches. Nothing in Redis is unrecoverable.

## Observability

- Structured logging via `log/slog` (JSON in production), every request
  tagged with an `X-Request-ID` correlation ID.
- `/healthz` is dependency-free liveness; `/readyz` aggregates registered
  dependency checks and returns 503 until all pass.
- Metrics endpoints and historical metrics storage arrive with the metrics
  module.

## Module dependency rules

Modules never import each other's internals, with two sanctioned
infrastructure exceptions feature modules may depend on:

- `modules/agents` (the hub): it is the transport to managed hosts, in the
  same category as the database pool. Runtime/files/terminal modules call
  `hub.Call` / `hub.OpenStream`.
- `modules/audit` (the recorder) and `modules/rbac` middleware factories:
  cross-cutting concerns injected at wiring time.

Everything else crosses module boundaries through consumer-owned interfaces
wired in `internal/app` (e.g. the hub's `ServerDirectory`, auth's
`UserStore`, rbac's `ServerGroupResolver`).

## Implementation notes

- **Refresh tokens rotate on every use**; replaying a rotated token revokes
  every session of the user (theft response) — see `auth.Service.Refresh`.
- **TOTP secrets are AES-256-GCM sealed** at rest with the configured
  encryption key; recovery codes and every opaque token (refresh, agent,
  PAT) are stored as SHA-256 digests only.
- **The migrator is embedded and forward-only** with checksum verification;
  down files exist for operators, never auto-applied.
- **Single-instance control plane** for now: the event bus and rate
  limiters are in-memory. Redis config exists so multi-instance fan-out can
  swap in behind the same bus interface — recorded here as the known
  scale-out boundary.
- **Docker exec/terminal uses the engine's hijacked connection.** The
  exec/attach endpoints stop speaking HTTP after the upgrade, which
  `net/http` cannot surface, so those requests are written directly onto a
  dialed socket (`providers/docker/exec.go`) and the raw conn is returned.
  Without a TTY the stream carries Docker's 8-byte stdout/stderr framing and
  is demultiplexed; with a TTY it is raw bytes plus a resize endpoint.
- **A console is not a terminal.** `TerminalProvider` starts a *new* shell
  beside the workload; `ConsoleProvider` attaches to the main process's own
  stdin/stdout. Console-driven software (game servers, REPL daemons) only
  takes commands on the latter — typing `stop` into a container shell would
  never reach a Minecraft server. The daemon supervisor keeps a stdin pipe
  open for the lifetime of the process; Docker containers are created with
  `OpenStdin` and attached via the same hijack path as exec. Runtimes that
  cannot accept input simply omit the interface, so `CapConsole` is absent
  and the UI renders a read-only log pane instead of an input box.
- **Docker images, volumes and networks are not runtimes.** They are
  Docker-specific object types with no lifecycle in common with a systemd
  unit or a daemon, so forcing them into the Runtime abstraction would
  corrupt it. They live behind their own `docker.resource.*` methods and the
  `dockerres` module instead.
- **Windows agents**: supported for development (daemon supervisor works,
  no host PTY, no systemd/docker socket); managed production hosts are
  Linux-first.
- **Bulk file transfer is streamed, never buffered.** Downloads and uploads
  ride dedicated agent streams: the control plane copies frames between the
  HTTP body and the stream, so a multi-gigabyte file never lands in server
  memory. `fs.read`/`fs.write` remain the JSON+base64 path for the editor
  and stay size-capped. Uploads commit via a temp file + rename, so an
  interrupted transfer cannot replace a good file.
- **Stream payloads are copied before enqueueing.** Frames are marshaled
  asynchronously by the write pump; passing a reused read buffer silently
  corrupts data in flight (this bit us on 12 MiB transfers). `Send` and
  `SendData` copy defensively so no caller can reintroduce it.
- **Archive extraction is hostile-input territory**: entry names are
  validated against zip-slip on raw slash-form names (host `IsAbs`
  semantics differ), symlink targets are checked, and both entry count and
  total extracted bytes are capped against decompression bombs.
- **Plugins are external processes, not loaded code.** A plugin is a
  manifest plus an executable that speaks line-delimited JSON on stdio; the
  agent spawns it per call. Go's `plugin` package would demand identical
  toolchains and lets a bad plugin corrupt the agent's address space, so
  Runix follows the Terraform/Vault model instead: isolation by process,
  any implementation language, and a plugin appears as an ordinary runtime
  type because it satisfies the same `Provider` interface.
- **Agent self-update requires a checksum.** The agent usually runs as
  root, so accepting a URL alone would be a remote-code-execution channel.
  The binary is verified against a mandatory SHA-256 before it replaces the
  running file (via rename, which is safe while executing) and the process
  exits so the supervisor starts the new build.
- **Permission keys stay dotted; names are for humans.** `server.files.write`
  remains the identifier stored in `role_permissions`/`grants` and checked
  in code — renaming it would be a data migration with no functional gain.
  The catalog carries a label, description and group per permission, which
  the API returns and the UI renders, so operators never read machine keys.
- **Backups are configuration, not a secrets vault.** Passwords, agent
  tokens and TOTP secrets are deliberately excluded, and import is additive
  (existing objects are skipped, never overwritten) so a restore cannot
  silently destroy live configuration.
- **Grant subjects are users, user groups and roles.** Roles bundle
  permissions globally; a role *grant* narrows extra permissions to a scope
  ("operators may restart runtimes on this server"). User groups group
  people independently of job function. Both are now reachable in the UI —
  a subject type that cannot be populated is worse than no subject type.
- **Runtime-scoped grants carry the server in their id**
  (`<serverId>/<type>/<runtimeId>`), so a grant for `docker/web` on one host
  never matches another host's container of the same name. Matching is
  exact: Docker accepts a name, a short id and a full id for the same
  container, but fuzzy matching inside an authorization decision is how
  subtle holes appear, so the UI always addresses runtimes by their
  canonical descriptor id instead.
- **The Redis bridge is optional.** With `RUNIX_REDIS_ADDR` set, each
  instance mirrors bus events through one channel and ignores its own
  echoes by origin id, which is what makes multi-instance deployments show
  consistent live state. Without it the platform runs exactly as before,
  single-instance.

## Roadmap

| Phase | Scope | Status |
|---|---|---|
| 1 | Foundation: runtime domain, config, logging, server/agent skeletons, CI | **done** |
| 2 | Persistence (PostgreSQL + embedded migrations), auth (JWT, MFA, PAT, sessions), users, RBAC (roles/groups/scoped grants), audit, settings | **done** |
| 3 | Agent transport (WSS RPC + streams), servers module, enrollment tokens, heartbeat + inventory, presence | **done** |
| 4 | Runtime providers: docker (engine API), compose (CLI), systemd (CLI), native daemon supervisor | **done** |
| 5 | Runtimes API, file manager, terminals, metrics history + live WS, dashboard, notifications, OpenAPI spec | **done** |
| 6 | Next.js frontend (login+MFA, dashboard, servers, runtimes, terminal, files, users/roles, audit, settings, account) | **done** |
| 6.1 | Runtime UX: per-type tabs, form-based daemon authoring, in-UI agent onboarding, in-place runtime editing (`update` capability + `Updater` interface), per-runtime detail page (live console, actions, browse files) | **done** |
| 6.2 | File manager: create/copy/cut/paste, chmod, tar.gz+zip compress/extract, streamed multi-file upload & download (drag & drop), directory tabs, toolbar + right-click context menu; operator-supplied agent tokens | **done** |
| 7 | Docker exec/attach streaming (connection hijack), image/volume/network management, compose file authoring, grants UI, install scripts | **done** |
| 8 | Scheduler (own cron engine), agent self-update, configuration backup/restore, plugin system (external providers), Redis-backed bus for HA; readable permission names; i18n with Russian | **done** |
| 9 | Registry auth for private images, alerting/notification channels, metrics downsampling, plugin distribution | next |

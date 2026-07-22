<div align="center">

# Runix

**One console for your whole fleet.**
Containers, system services and custom daemons — managed the same way, on every host.

<img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25">
<img src="https://img.shields.io/badge/Next.js-15-000000?logo=nextdotjs&logoColor=white" alt="Next.js 15">
<img src="https://img.shields.io/badge/PostgreSQL-17-4169E1?logo=postgresql&logoColor=white" alt="PostgreSQL 17">
<img src="https://img.shields.io/badge/platforms-linux%20amd64%20%7C%20arm64-555" alt="Platforms">
<img src="https://img.shields.io/badge/install-one%20command-3fb950" alt="One-command install">

<br>

<img src="docs/images/dashboard.png" alt="Runix dashboard" width="100%">

</div>

---

## What it is

Runix manages servers the way you'd want to: you say *restart that thing*, and
it doesn't matter whether "that thing" is a Docker container, a Compose
project, a systemd unit or a plain process you wrote yourself.

Everything is a **Runtime** with the same lifecycle — start, stop, restart,
logs, metrics, a shell — so the interface stays the same as your stack changes.
Adding support for something new (Podman, Kubernetes, a game-server panel)
means implementing one Go interface, and the entire UI and API pick it up.

It ships as **two static binaries and no runtime dependencies**. The control
plane serves its own web UI from inside the executable; agents dial *out* to it
over WebSocket, so managed hosts need no inbound ports, no VPN and no
public IP.

## Install

One command. It asks what the host should be, then does the rest:

```sh
curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh -o install.sh
chmod +x install.sh
./install.sh
```

```
What should this host run?
  1) Control plane + agent (single-host install)
  2) Control plane only
  3) Agent only — join a control plane running elsewhere
```

Pick **1** and you get a working system from an empty machine: it provisions
PostgreSQL in Docker, starts the control plane, registers the host with it, and
installs the agent with a token it mints itself. Then open the URL it prints
and log in.

<details>
<summary><b>Unattended installs, upgrades and options</b></summary>

<br>

Every question is also a flag, so the same script works from Ansible or CI:

```sh
# Single-host install, no questions asked
curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh -o install.sh
chmod +x install.sh
./install.sh --role all-in-one --yes

# Agent joining an existing control plane
curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh -o install.sh
chmod +x install.sh
./install.sh --role agent --url https://runix.example.com --token rnx_agt_...
```

`-y` takes the recommended default for anything you didn't pass. Values with no
safe default — the role, an agent's URL and token — fail loudly instead of
being guessed.

**Upgrading** is the same command. It reads what the host already is and doesn't
ask again:

```sh
curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh -o install.sh
chmod +x install.sh
./install.sh -y
```

The JWT and encryption secrets, admin password, database password and the
agent's enrollment token are all preserved. (Rotating the first two would
invalidate every session and make stored MFA secrets unreadable.)

Everything lives under one directory:

```
/opt/runix/bin/          runix-server, runix-agent
/opt/runix/etc/          server.env, agent.env  (0600 — secrets live here)
/opt/runix/postgres/     docker-compose.yml, .env, data/    (control plane)
/opt/runix/agent/        supervised daemon state            (agent)
```

Binaries are downloaded for your architecture and verified against the
release's `SHA256SUMS`; a mismatch aborts before anything is installed.

| Flag | Purpose |
|---|---|
| `--dsn` | Use a PostgreSQL you already run instead of provisioning one |
| `--pg-port` | Host port for the provisioned database (default 5432) |
| `--port`, `--public-url` | Control-plane listen port and the URL browsers use |
| `--version`, `--repo` | Pin a release, or install from a fork |
| `--github-token` | Install from a private repository |
| `--prefix` | Change the install root |
| `--no-start` | Configure everything, start nothing |

`sh install.sh --help` lists them all.

</details>

## What you get

### Every workload, one interface

Native daemons, Docker containers, Compose projects and systemd units, each in
its own tab, all with the same actions. Runix supervises native daemons itself
— restart policies, backoff, log capture — so a plain binary gets the same
treatment as a container.

<img src="docs/images/runtimes.png" alt="Runtimes" width="100%">

### A console that actually takes input

Not a log tail, and not a shell *next to* your process — the process's own
stdin. Type `stop` into a game server and the game server sees it. Copy and
paste work like they do in a terminal.

<img src="docs/images/console.png" alt="Interactive runtime console" width="100%">

### Per-host metrics and inventory

CPU, memory, disk and load, live and historical, plus the hardware and OS
details the agent reports on every heartbeat.

<img src="docs/images/server-overview.png" alt="Server overview" width="100%">

### A real file manager

Browse, edit with syntax highlighting, upload by drag-and-drop, download whole
directories as `tar.gz`, create archives, change permissions — with tabs, a
right-click context menu, and multi-select. Transfers stream frame by frame, so
a large file is never buffered in the control plane's memory.

<img src="docs/images/files.png" alt="File manager" width="100%">

### Terminals, into the host or into a container

A real PTY on the host, or a shell inside a running container, over the same
outbound connection the agent already holds open.

<img src="docs/images/terminal.png" alt="Host terminal" width="100%">

### Docker, beyond containers

Images, volumes and networks with disk usage and pruning — because they aren't
runtimes and pretending otherwise would have bent the model out of shape.

<img src="docs/images/docker.png" alt="Docker resources" width="100%">

### Scheduling, with a cron engine of its own

Restart something nightly, run a command weekly. Multiple control-plane
instances can share a database without ever double-running a task.

<img src="docs/images/schedule.png" alt="Scheduled tasks" width="100%">

### Access control that goes down to a single container

Grant a user, a role or a group access to the whole fleet, one server, or one
specific runtime on one specific host. Everything is a dropdown; nothing asks
you to paste a UUID.

<img src="docs/images/grants.png" alt="Permission grants" width="100%">

### Audit log

Who did what, to which resource, from which IP — recorded for every mutating
action.

<img src="docs/images/audit.png" alt="Audit log" width="100%">

### And the rest

- **Auth** — argon2id, JWT with rotating refresh tokens and reuse detection,
  TOTP MFA with recovery codes, personal access tokens
- **Backup/restore** of configuration, deliberately without secrets
- **Agent self-update**, refusing any download without a matching checksum
- **Plugins** — external processes speaking line-delimited JSON on stdio can
  register entirely new runtime types
- **English and Russian** UI; adding a language is one file

## How it works

```
    Browser ──────► runix-server ──────► PostgreSQL
                    (control plane)      Redis (optional, multi-instance)
                          ▲
                          │  agents dial OUT over WebSocket
                          │  (one connection: RPC + byte streams)
            ┌─────────────┼─────────────┐
            │             │             │
        runix-agent   runix-agent   runix-agent
        docker        systemd       daemons
```

- **Agents connect outbound.** Managed hosts need no open ports. One connection
  multiplexes correlated RPC calls and byte streams (logs, terminals, file
  transfers) at once.
- **The runtime abstraction is small on purpose.** A core interface every
  provider implements, plus optional capability interfaces (`LogStreamer`,
  `Execer`, `TerminalProvider`, `ConsoleProvider`, …) it opts into. The
  capability set travels to the UI, which renders only the actions that exist —
  so no button is ever a "not supported" error waiting to happen.
- **Clean architecture, vertical slices.** Domain has no imports; feature
  modules own their handler/service/repository; a composition root wires them
  together.

Design decisions and their rationale are in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md); the REST API is specified in
[api/openapi.yaml](api/openapi.yaml).

## Configuration

Environment-based, on both binaries.

<details>
<summary><b><code>runix-server</code></b></summary>

<br>

| Variable | Default | Description |
|---|---|---|
| `RUNIX_ENV` | `production` | `development`, `production` or `test` |
| `RUNIX_HTTP_ADDR` | `:8080` | HTTP listen address |
| `RUNIX_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown grace period |
| `RUNIX_CORS_ORIGINS` | dev: `http://localhost:3000` | Browser origins allowed for API + WS. Not needed when the UI is served from the binary |
| `RUNIX_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RUNIX_LOG_FORMAT` | `json` | `json` or `text` |
| `RUNIX_DATABASE_DSN` | — (required) | PostgreSQL DSN |
| `RUNIX_REDIS_ADDR` | — | Set to share live events across multiple control-plane instances |
| `RUNIX_JWT_SECRET` | generated in dev | JWT signing secret, min 32 chars (required in production) |
| `RUNIX_ENCRYPTION_KEY` | generated in dev | At-rest secret encryption key, min 16 chars (required in production) |
| `RUNIX_ACCESS_TOKEN_TTL` | `15m` | Access token lifetime |
| `RUNIX_REFRESH_TOKEN_TTL` | `168h` | Refresh token lifetime |
| `RUNIX_REMEMBER_TOKEN_TTL` | `720h` | Refresh lifetime with "remember me" |
| `RUNIX_ADMIN_PASSWORD` | generated | Initial admin password on first boot |

</details>

<details>
<summary><b><code>runix-agent</code></b></summary>

<br>

| Variable | Default | Description |
|---|---|---|
| `RUNIX_AGENT_SERVER_URL` | — (required) | Control-plane URL (`https://` or `wss://`) |
| `RUNIX_AGENT_TOKEN` | — (required) | Per-server enrollment token |
| `RUNIX_AGENT_HEARTBEAT_INTERVAL` | `30s` | Heartbeat period (min `1s`) |
| `RUNIX_AGENT_DATA_DIR` | `/var/lib/runix-agent` | Daemon supervisor state directory. The installer sets this to `/opt/runix/agent` on new installs, and preserves an existing path on upgrade |
| `RUNIX_AGENT_LOG_LEVEL` | `info` | Log level |
| `RUNIX_AGENT_LOG_FORMAT` | `json` | Log format |

</details>

## Development

```sh
make build          # host binaries into bin/
make test           # unit tests
make vet            # go vet
make web-embed      # export the UI and stage it for embedding
make release        # UI + static linux amd64/arm64 binaries into dist/
```

Bring up the backing services and run the control plane:

```sh
docker compose -f docker-compose.dev.yml up -d    # PostgreSQL + Redis

RUNIX_ENV=development \
RUNIX_DATABASE_DSN=postgres://runix:runix@127.0.0.1:5432/runix \
./bin/runix-server
```

On an empty database it applies its migrations, seeds the built-in roles
(admin/operator/viewer) and creates an `admin` user — the password comes from
`RUNIX_ADMIN_PASSWORD` or is generated and logged once. First login forces a
change.

For UI work, run Next's dev server for hot reload:

```sh
cd web && npm ci && npm run dev     # http://localhost:3000
```

It talks to the API on `:8080` (`web/.env.development`), which development mode
allows automatically. In a release build the UI is exported to static files and
embedded into `runix-server` instead — a checkout that hasn't built it still
compiles, and serves a page saying so.

Both binaries are `CGO_ENABLED=0` static builds for `linux/amd64` and
`linux/arm64`. Tagging `v*` runs the release workflow, which builds the UI,
embeds it, and publishes the binaries with `SHA256SUMS` and `install.sh`.

## Repository layout

```
cmd/runix-server/      control-plane entrypoint
cmd/runix-agent/       agent entrypoint (registers runtime providers)
api/openapi.yaml       REST API specification (OpenAPI 3.1)
internal/domain/       pure domain model (the runtime abstraction)
internal/protocol/     control-plane ⇄ agent wire protocol
internal/app/          composition root: wiring, seeding, workers
internal/modules/      vertical feature slices (auth, users, rbac, audit,
                       servers, agents hub, runtimes, files, terminal,
                       metrics, dashboard, scheduler, backup, plugins, …)
internal/agent/        agent process: session, RPC handlers, collectors,
                       providers/ (docker, compose, systemd, daemon)
internal/platform/     shared kernel: config, logging, crypto, tokens, db,
                       bus, cron, http helpers, rate limiting
internal/server/       HTTP transport assembly (middleware, routing, lifecycle)
internal/webui/        the exported UI, embedded into the binary
migrations/            embedded PostgreSQL migrations
web/                   Next.js frontend (TypeScript, Tailwind, TanStack Query)
web/src/i18n/          UI translations (en, ru) — add a locale by adding a file
scripts/install.sh     the installer (control plane, agent, or both)
docs/                  architecture and design documents
```

## Status

Pre-1.0 and moving fast, but everything shown above works today and is
exercised end to end against real PostgreSQL, Docker and systemd.

Next up: registry authentication for private images, alerting and notification
channels, metrics downsampling, and plugin distribution.

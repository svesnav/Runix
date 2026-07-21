# Runix

Runix is a modern infrastructure management platform: one consistent
interface for managing servers, containers, system services and custom
daemons across an entire fleet.

Everything Runix manages — a Docker container, a Compose project, a systemd
unit, a Runix-native daemon, a future Kubernetes workload — is a **Runtime**
with the same lifecycle. Users never care about the implementation
underneath.

> **Status: functional backend, pre-release.** Working today: PostgreSQL
> persistence with embedded migrations, JWT auth with TOTP MFA + recovery
> codes + PATs, enterprise RBAC (roles, user groups, scoped grants), audit
> log, server inventory with agent enrollment, live agent transport
> (outbound WSS with RPC + streams), four runtime providers (docker,
> compose, systemd, native daemon supervisor), remote file manager,
> terminals, metrics history + live feeds, dashboard, settings, a cron
> scheduler, configuration backup/restore, agent self-update and a plugin
> system for external runtime providers — all described in
> [api/openapi.yaml](api/openapi.yaml) — plus a Next.js web UI (login +
> MFA, dashboard, servers, runtimes, Docker resources, file manager with
> Monaco, xterm terminals, users/roles/grants, schedule, audit, settings)
> available in English and Russian.
> See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Components

| Binary | Role |
|---|---|
| `runix-server` | Control plane: REST API, WebSocket, PostgreSQL, Redis |
| `runix-agent` | Per-host agent: heartbeat, metrics, runtime providers |

Both are single static Go binaries built with `CGO_ENABLED=0` for
`linux/amd64` and `linux/arm64`.

## Building

```sh
make build        # host binaries into bin/
make test         # unit tests
make vet          # go vet
make release      # static linux amd64 + arm64 binaries into dist/
```

## Installing

One command, and it asks what this host should be:

```sh
curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh | sudo sh
```

```
What should this host run?
  1) Control plane + agent (single-host install)
  2) Control plane only
  3) Agent only — join a control plane running elsewhere
```

Picking **1** gives a working system from nothing: it provisions
PostgreSQL, starts the control plane, registers this host with it, and
installs the agent using a token it mints itself — no copy-pasting.

Every answer is also a flag, so the same script runs unattended:

```sh
# Single-host install, no questions
curl -fsSL .../install.sh | sudo sh -s -- --role all-in-one --yes

# Agent joining an existing control plane
curl -fsSL .../install.sh | sudo sh -s -- --role agent \
    --url https://runix.example.com --token rnx_agt_...
```

`-y` takes the recommended default for anything not passed. Values with no
safe default — the role, an agent's URL and token — still fail loudly
rather than guessing.

There is one installer and one release asset. It downloads the matching
`linux/amd64` or `linux/arm64` binaries from the GitHub release, verifies
them against the release's `SHA256SUMS`, installs under `/opt/runix`, and
sets up systemd services.

Re-running it upgrades in place. It reads what the host already is and
does not ask again, so an upgrade is just:

```sh
curl -fsSL .../install.sh | sudo sh -s -- -y
```

The JWT and encryption secrets, the admin password, the database password
and the agent's enrollment token are all preserved — rotating the first two
would invalidate every session and make stored TOTP secrets unreadable.

The install root is a single directory:

```
/opt/runix/bin/          runix-server, runix-agent
/opt/runix/etc/          server.env, agent.env (0600 — secrets live here)
/opt/runix/postgres/     docker-compose.yml, .env, data/   (server only)
/opt/runix/agent/        supervised daemon state           (agent only)
```

For control-plane roles it provisions PostgreSQL by default: it writes a
Compose file, generates a password, waits for `pg_isready`, and derives
the DSN. The container is published on `127.0.0.1` only. Pass `--dsn` to
use a database you already run. Use `--pg-port` if 5432 is taken.

Useful flags: `--server-binary` / `--agent-binary` install local builds
instead of downloading; `--version` pins a release; `--repo OWNER/NAME`
and `--github-token` point at a fork or a private repository; `--prefix`
changes the install root; `--no-start` configures without starting.
`sh install.sh --help` lists them all.

Hosts installed before the `/opt` layout have their config migrated from
`/etc/runix` automatically, keeping the agent's existing data directory so
supervised daemons are not orphaned.

Tagging `v*` runs `.github/workflows/release.yml`, which builds all four
binaries, publishes `SHA256SUMS`, and attaches `install.sh`. The asset
names (`runix-server_linux_amd64`, …) are the installer's contract —
renaming them breaks every installed host.

## Quick start (development)

```sh
docker compose -f docker-compose.dev.yml up -d   # PostgreSQL + Redis

RUNIX_ENV=development \
RUNIX_DATABASE_DSN=postgres://runix:runix@127.0.0.1:5432/runix \
./bin/runix-server
```

On an empty database the server seeds the built-in roles
(admin/operator/viewer) and creates an `admin` user; the password comes from
`RUNIX_ADMIN_PASSWORD` or is generated and logged once. First login forces a
password change.

To attach a server: `POST /api/v1/servers` returns a one-time agent token,
then on the managed host:

```sh
RUNIX_AGENT_SERVER_URL=https://runix.example.com \
RUNIX_AGENT_TOKEN=rnx_agt_... \
./runix-agent
```

### Web UI

```sh
cd web && npm ci && npm run dev   # http://localhost:3000
```

The dev server talks to the API at `http://127.0.0.1:8080`
(`web/.env.development`); in development mode the backend allows that
origin automatically. For production, `npm run build && npm start` and set
`NEXT_PUBLIC_API_URL` (or serve same-origin behind a reverse proxy and set
`RUNIX_CORS_ORIGINS` accordingly).

## Configuration

Configuration is environment-based.

### runix-server

| Variable | Default | Description |
|---|---|---|
| `RUNIX_ENV` | `production` | `development`, `production` or `test` |
| `RUNIX_HTTP_ADDR` | `:8080` | HTTP listen address |
| `RUNIX_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown grace period |
| `RUNIX_CORS_ORIGINS` | dev: `http://localhost:3000` | Comma-separated browser origins allowed for API + WS |
| `RUNIX_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `RUNIX_LOG_FORMAT` | `json` | `json` or `text` |
| `RUNIX_DATABASE_DSN` | — (required) | PostgreSQL DSN |
| `RUNIX_REDIS_ADDR` | — | Redis address; set it to share live events across multiple control-plane instances |
| `RUNIX_JWT_SECRET` | generated in dev | JWT signing secret, min 32 chars (required in production) |
| `RUNIX_ENCRYPTION_KEY` | generated in dev | At-rest secret encryption key, min 16 chars (required in production) |
| `RUNIX_ACCESS_TOKEN_TTL` | `15m` | Access token lifetime |
| `RUNIX_REFRESH_TOKEN_TTL` | `168h` | Refresh token lifetime |
| `RUNIX_REMEMBER_TOKEN_TTL` | `720h` | Refresh lifetime with "remember me" |
| `RUNIX_ADMIN_PASSWORD` | generated | Initial admin password on first boot |

### runix-agent

| Variable | Default | Description |
|---|---|---|
| `RUNIX_AGENT_SERVER_URL` | — (required) | Control-plane URL (`https://` or `wss://`) |
| `RUNIX_AGENT_TOKEN` | — | Per-server agent token (from server registration) |
| `RUNIX_AGENT_HEARTBEAT_INTERVAL` | `30s` | Heartbeat period (min `1s`) |
| `RUNIX_AGENT_DATA_DIR` | `/var/lib/runix-agent` | Daemon supervisor state directory |
| `RUNIX_AGENT_LOG_LEVEL` | `info` | Log level |
| `RUNIX_AGENT_LOG_FORMAT` | `json` | Log format |

## Repository layout

```
cmd/runix-server/      control-plane entrypoint
cmd/runix-agent/       agent entrypoint (registers runtime providers)
api/openapi.yaml       REST API specification (OpenAPI 3.1)
internal/domain/       pure domain model (runtime abstraction)
internal/protocol/     control-plane ⇄ agent wire protocol
internal/app/          composition root: wiring, seeding, workers
internal/modules/      vertical feature slices (auth, users, rbac, audit,
                       servers, agents hub, runtimes, files, terminal,
                       metrics, dashboard, notifications, settings, health)
internal/agent/        agent process: session, RPC handlers, collectors,
                       providers/ (docker, compose, systemd, daemon)
internal/platform/     shared kernel: config, logging, crypto, tokens, db,
                       bus, http helpers, rate limiting
internal/server/       HTTP transport assembly (middleware, routing, lifecycle)
migrations/            embedded PostgreSQL migrations
web/                   Next.js frontend (TypeScript, Tailwind, TanStack Query)
web/src/i18n/          UI translations (en, ru) — add a locale by adding a file
scripts/               install.sh — the installer (control plane, agent, or both)
docs/                  architecture and design documents
```

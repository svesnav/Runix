#!/bin/sh
# Runix installer — the one command that sets a host up.
#
#   curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh | sudo sh
#
# Asks what this host should be (control plane, agent, or both), checks
# the prerequisites, installs under /opt/runix and wires up systemd.
#
# Re-running upgrades in place: the binaries are replaced and the
# services restarted, while the existing configuration — above all the
# JWT and encryption secrets — is kept. Rotating those would invalidate
# every session and make stored TOTP secrets unreadable.
#
# Non-interactive use (CI, config management) is supported: pass --role
# and the values you would have typed, and nothing is prompted.
#
# POSIX sh on purpose: minimal images often have no bash.

set -eu

PREFIX=${RUNIX_PREFIX:-/opt/runix}
REPO=${RUNIX_REPO:-svesnav/Runix}
VERSION=${RUNIX_VERSION:-latest}
DOWNLOAD_BASE=${RUNIX_DOWNLOAD_BASE:-}
GITHUB_TOKEN=${RUNIX_GITHUB_TOKEN:-${GITHUB_TOKEN:-}}

ROLE=""
ASSUME_YES=0
NO_START=0
SERVER_BIN=""
AGENT_BIN=""

# Control plane.
SERVER_USER=${RUNIX_SERVER_USER:-runix}
DB_MODE=""            # docker | existing
DSN=${RUNIX_DATABASE_DSN:-}
HTTP_PORT=""
PUBLIC_URL=""
ADMIN_PASSWORD=${RUNIX_ADMIN_PASSWORD:-}
PG_IMAGE=${RUNIX_POSTGRES_IMAGE:-postgres:17-alpine}
PG_PORT=""
PG_DB=${RUNIX_POSTGRES_DB:-runix}
PG_USER=${RUNIX_POSTGRES_USER:-runix}
PG_CONTAINER=${RUNIX_POSTGRES_CONTAINER:-runix-postgres}
PG_PASSWORD=""

# Agent.
AGENT_USER=${RUNIX_AGENT_USER:-root}
SERVER_URL=${RUNIX_AGENT_SERVER_URL:-}
TOKEN=${RUNIX_AGENT_TOKEN:-}
DATA_DIR=${RUNIX_AGENT_DATA_DIR:-}

usage() {
    cat <<EOF
Runix installer

Run with no arguments to be asked what you want. Every answer also has a
flag, so the same script works unattended.

Options:
  --role ROLE          all-in-one | server | agent
  --db MODE            docker | existing        (control-plane roles)
  --dsn DSN            PostgreSQL DSN           (implies --db existing)
  --pg-port PORT       Port for the provisioned database (default 5432)
  --pg-image IMAGE     PostgreSQL image (default $PG_IMAGE)
  --port PORT          Control-plane HTTP port (default 8080)
  --public-url URL     Where browsers reach the UI (sets CORS)
  --admin-password PW  Initial admin password (default: generated)
  --url URL            Control-plane URL        (agent role)
  --token TOKEN        Enrollment token         (agent role)
  --data-dir PATH      Agent state directory (default \$PREFIX/agent)
  --server-user USER   Control-plane service user (default $SERVER_USER)
  --agent-user USER    Agent service user (default $AGENT_USER)
  --server-binary PATH Install a local control-plane build
  --agent-binary PATH  Install a local agent build
  --version VERSION    Release to install (default: latest)
  --repo OWNER/NAME    Source repository (default: $REPO)
  --github-token TOK   Token for a private repository
  --prefix PATH        Install root (default: $PREFIX)
  --no-start           Install and configure, but do not start services
  -y, --yes            Take defaults, do not ask
  -h, --help           Show this help

Environment equivalents: RUNIX_PREFIX, RUNIX_VERSION, RUNIX_REPO,
RUNIX_GITHUB_TOKEN, RUNIX_DATABASE_DSN, RUNIX_ADMIN_PASSWORD,
RUNIX_AGENT_SERVER_URL, RUNIX_AGENT_TOKEN, RUNIX_POSTGRES_PORT.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --role) ROLE="$2"; shift 2 ;;
        --db) DB_MODE="$2"; shift 2 ;;
        --dsn) DSN="$2"; DB_MODE=existing; shift 2 ;;
        --pg-port) PG_PORT="$2"; shift 2 ;;
        --pg-image) PG_IMAGE="$2"; shift 2 ;;
        --port) HTTP_PORT="$2"; shift 2 ;;
        --public-url) PUBLIC_URL="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --url) SERVER_URL="$2"; shift 2 ;;
        --token) TOKEN="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --server-user) SERVER_USER="$2"; shift 2 ;;
        --agent-user) AGENT_USER="$2"; shift 2 ;;
        --server-binary) SERVER_BIN="$2"; shift 2 ;;
        --agent-binary) AGENT_BIN="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --repo) REPO="$2"; shift 2 ;;
        --github-token) GITHUB_TOKEN="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        --no-start) NO_START=1; shift ;;
        -y|--yes) ASSUME_YES=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[ -n "$DOWNLOAD_BASE" ] || DOWNLOAD_BASE="https://github.com/$REPO/releases"
BIN_DIR="$PREFIX/bin"
CONFIG_DIR="$PREFIX/etc"
PG_DIR="$PREFIX/postgres"
SERVER_ENV="$CONFIG_DIR/server.env"
AGENT_ENV="$CONFIG_DIR/agent.env"

# ------------------------------------------------------------------ output

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    C_B=$(printf '\033[1m'); C_DIM=$(printf '\033[2m'); C_OK=$(printf '\033[32m')
    C_WARN=$(printf '\033[33m'); C_ERR=$(printf '\033[31m'); C_0=$(printf '\033[0m')
else
    C_B=""; C_DIM=""; C_OK=""; C_WARN=""; C_ERR=""; C_0=""
fi

say()   { echo "  ${C_DIM}$*${C_0}"; }
ok()    { echo "  ${C_OK}✓${C_0} $*"; }
warn()  { echo "  ${C_WARN}!${C_0} $*"; }
fail()  { echo "${C_ERR}[runix] error:${C_0} $*" >&2; exit 1; }
head2() { echo; echo "${C_B}$*${C_0}"; }

# --------------------------------------------------------------- interaction

# `curl … | sh` leaves stdin pointing at the pipe, so reading answers from
# it would consume the script itself. Reattach the terminal when there is
# one; without it the run is non-interactive and every value must come
# from a flag.
#
# The probe runs in a subshell on purpose: a failed `exec <` redirection
# is fatal to a non-interactive shell, so testing it directly would kill
# the installer without a word on any host that has /dev/tty present but
# no controlling terminal (cron, CI, docker exec -T).
INTERACTIVE=1
if [ ! -t 0 ]; then
    if (exec </dev/tty) >/dev/null 2>&1; then
        exec </dev/tty
    else
        INTERACTIVE=0
    fi
fi
# -y means unattended: take the default for anything not given on the
# command line rather than stopping to ask. Values with no safe default
# (the role, an agent's URL and token) still fail loudly.
if [ "$ASSUME_YES" -eq 1 ]; then
    INTERACTIVE=0
fi

ask() {
    # ask VARNAME "question" "default"
    _var=$1; _q=$2; _def=${3:-}
    if [ "$INTERACTIVE" -eq 0 ]; then
        eval "$_var=\$_def"
        return
    fi
    if [ -n "$_def" ]; then
        printf '  %s [%s]: ' "$_q" "$_def"
    else
        printf '  %s: ' "$_q"
    fi
    read -r _ans || _ans=""
    [ -n "$_ans" ] || _ans=$_def
    eval "$_var=\$_ans"
}

ask_secret() {
    _var=$1; _q=$2
    if [ "$INTERACTIVE" -eq 0 ]; then
        eval "$_var=''"
        return
    fi
    printf '  %s: ' "$_q"
    if command -v stty >/dev/null 2>&1; then
        _old=$(stty -g 2>/dev/null || echo)
        stty -echo 2>/dev/null || true
        read -r _ans || _ans=""
        if [ -n "$_old" ]; then
            stty "$_old" 2>/dev/null || true
        else
            stty echo 2>/dev/null || true
        fi
        echo
    else
        read -r _ans || _ans=""
    fi
    eval "$_var=\$_ans"
}

confirm() {
    [ "$ASSUME_YES" -eq 1 ] && return 0
    [ "$INTERACTIVE" -eq 0 ] && return 0
    printf '  %s [Y/n]: ' "$1"
    read -r _a || _a=""
    case "$_a" in
        n|N|no|NO|No) return 1 ;;
        *) return 0 ;;
    esac
}

choose() {
    # choose VARNAME "question" "value:label" ...
    # The first option is the recommended one, and is what an unattended
    # run gets when the caller did not pass the matching flag.
    _var=$1; _q=$2; shift 2
    if [ "$INTERACTIVE" -eq 0 ]; then
        eval "_cur=\${$_var:-}"
        if [ -z "$_cur" ]; then
            _first=$1
            eval "$_var=\${_first%%:*}"
        fi
        return
    fi
    [ -n "$_q" ] && echo "  $_q"
    _i=0
    for _opt in "$@"; do
        _i=$((_i + 1))
        echo "    $_i) ${_opt#*:}"
    done
    while :; do
        printf '  choice [1]: '
        read -r _n || _n=""
        [ -n "$_n" ] || _n=1
        case "$_n" in
            ''|*[!0-9]*) echo "    enter a number"; continue ;;
        esac
        if [ "$_n" -ge 1 ] && [ "$_n" -le "$#" ]; then
            _i=0
            for _opt in "$@"; do
                _i=$((_i + 1))
                if [ "$_i" -eq "$_n" ]; then
                    eval "$_var=\${_opt%%:*}"
                    return
                fi
            done
        fi
        echo "    pick 1-$#"
    done
}

random_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# read_env FILE KEY — a value from an existing env file, or empty.
read_env() {
    if [ -f "$1" ]; then
        sed -n "s/^$2=//p" "$1" | head -n1
    fi
}

port_busy() {
    # Braced so the following bracket is not read as an array subscript.
    _p="${1}"
    if command -v ss >/dev/null 2>&1; then
        if ss -ltn 2>/dev/null | grep -q ":${_p}[[:space:]]"; then
            return 0
        fi
    elif command -v netstat >/dev/null 2>&1; then
        if netstat -ltn 2>/dev/null | grep -q ":${_p}[[:space:]]"; then
            return 0
        fi
    fi
    return 1
}

# ------------------------------------------------------------------ preflight

[ "$(id -u)" -eq 0 ] || fail "run as root (try: sudo sh $0 ...)"
[ "$(uname -s)" = Linux ] || fail "Runix hosts are Linux only"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) fail "unsupported architecture: $ARCH (amd64 and arm64 are built)" ;;
esac

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 \
    || fail "need curl or wget"

HOSTNAME_S=$(hostname 2>/dev/null || echo runix-host)
HAS_SYSTEMD=0
[ -d /run/systemd/system ] && HAS_SYSTEMD=1
HAS_DOCKER=0
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    HAS_DOCKER=1
fi
COMPOSE=""
if [ "$HAS_DOCKER" -eq 1 ]; then
    if docker compose version >/dev/null 2>&1; then
        COMPOSE="docker compose"
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE="docker-compose"
    fi
fi

# Releases before the /opt layout kept config in /etc/runix. Carry those
# files forward so an upgrade keeps its secrets and enrollment token.
migrate_legacy() {
    _legacy=$1; _new=$2
    if [ ! -f "$_new" ] && [ -f "$_legacy" ]; then
        mkdir -p "$CONFIG_DIR"
        cp "$_legacy" "$_new"
        chmod 0600 "$_new"
        ok "migrated existing config from $_legacy"
    fi
}
migrate_legacy /etc/runix/server.env "$SERVER_ENV"
migrate_legacy /etc/runix/agent.env "$AGENT_ENV"

HAVE_SERVER=0; [ -f "$SERVER_ENV" ] && HAVE_SERVER=1
HAVE_AGENT=0;  [ -f "$AGENT_ENV" ] && HAVE_AGENT=1

echo
echo "${C_B}Runix installer${C_0}  ${C_DIM}($REPO, $VERSION)${C_0}"
echo "${C_DIM}────────────────────────────────────────────────${C_0}"
echo "  host          $HOSTNAME_S  (linux/$ARCH)"
if [ "$HAS_SYSTEMD" -eq 1 ]; then ok "systemd"; else warn "no systemd — services must be started by hand"; fi
if [ "$HAS_DOCKER" -eq 1 ]; then
    if [ -n "$COMPOSE" ]; then ok "docker + compose"; else warn "docker without compose plugin"; fi
else
    warn "no docker — cannot provision PostgreSQL, and Docker runtimes will be unavailable"
fi
if [ "$HAVE_SERVER" -eq 1 ] || [ "$HAVE_AGENT" -eq 1 ]; then
    _found=""
    [ "$HAVE_SERVER" -eq 1 ] && _found="control plane"
    [ "$HAVE_AGENT" -eq 1 ] && _found="${_found:+$_found + }agent"
    ok "existing install at $PREFIX ($_found) — this upgrades it, keeping config"
fi

# ---------------------------------------------------------------- questions

# An upgrade should not re-ask what this host already is.
if [ -z "$ROLE" ]; then
    if [ "$HAVE_SERVER" -eq 1 ] && [ "$HAVE_AGENT" -eq 1 ]; then
        ROLE=all-in-one
    elif [ "$HAVE_SERVER" -eq 1 ]; then
        ROLE=server
    elif [ "$HAVE_AGENT" -eq 1 ]; then
        ROLE=agent
    fi
    [ -n "$ROLE" ] && say "keeping this host's existing role: $ROLE"
fi

if [ -z "$ROLE" ]; then
    head2 "What should this host run?"
    if [ "$INTERACTIVE" -eq 0 ]; then
        fail "--role is required in non-interactive mode (all-in-one|server|agent)"
    fi
    choose ROLE "" \
        "all-in-one:Control plane + agent (single-host install)" \
        "server:Control plane only" \
        "agent:Agent only — join a control plane running elsewhere"
fi
case "$ROLE" in
    all-in-one|server|agent) ;;
    *) fail "unknown role: $ROLE (all-in-one, server, agent)" ;;
esac

WANT_SERVER=0; WANT_AGENT=0
case "$ROLE" in
    all-in-one) WANT_SERVER=1; WANT_AGENT=1 ;;
    server) WANT_SERVER=1 ;;
    agent) WANT_AGENT=1 ;;
esac

if [ "$WANT_SERVER" -eq 1 ]; then
    # Existing values are the defaults, so an upgrade needs no answers.
    _old_dsn=$(read_env "$SERVER_ENV" RUNIX_DATABASE_DSN)
    _old_addr=$(read_env "$SERVER_ENV" RUNIX_HTTP_ADDR)
    _old_cors=$(read_env "$SERVER_ENV" RUNIX_CORS_ORIGINS)
    [ -n "$DSN" ] || DSN=$_old_dsn

    if [ -z "$DB_MODE" ]; then
        if [ -n "$DSN" ]; then
            # Already pointed at a database: keep using it. A previously
            # provisioned container is recognised by its compose file.
            if [ -f "$PG_DIR/docker-compose.yml" ]; then DB_MODE=docker; else DB_MODE=existing; fi
        elif [ -n "$COMPOSE" ]; then
            head2 "Database"
            choose DB_MODE "" \
                "docker:Run PostgreSQL for me, in Docker Compose (recommended)" \
                "existing:Use a PostgreSQL server I already have"
        else
            warn "docker compose is unavailable, so PostgreSQL cannot be provisioned here"
            DB_MODE=existing
        fi
    fi

    if [ "$DB_MODE" = docker ]; then
        [ -n "$COMPOSE" ] || fail "docker compose is required to provision PostgreSQL
  install it (https://docs.docker.com/engine/install/), or pass --dsn to
  point at a database you already have"
        if [ -z "$PG_PORT" ]; then
            PG_PORT=$(read_env "$PG_DIR/.env" POSTGRES_PORT)
            [ -n "$PG_PORT" ] || PG_PORT=${RUNIX_POSTGRES_PORT:-5432}
            if [ ! -f "$PG_DIR/.env" ] && port_busy "$PG_PORT"; then
                warn "port $PG_PORT is already in use on this host"
                ask PG_PORT "port for the Runix database" 5433
            fi
        fi
    else
        if [ -z "$DSN" ]; then
            head2 "Database"
            if [ "$INTERACTIVE" -eq 0 ]; then
                fail "a database DSN is required in non-interactive mode (pass --dsn)"
            fi
            say "example: postgres://runix:secret@127.0.0.1:5432/runix?sslmode=disable"
            ask DSN "PostgreSQL DSN" ""
        fi
        [ -n "$DSN" ] || fail "a DSN is required when not provisioning PostgreSQL"
        case "$DSN" in
            postgres://*|postgresql://*) ;;
            *) fail "that does not look like a PostgreSQL DSN: $DSN" ;;
        esac
    fi

    if [ -z "$HTTP_PORT" ]; then
        if [ -n "$_old_addr" ]; then
            HTTP_PORT=${_old_addr##*:}
        else
            head2 "Control plane"
            ask HTTP_PORT "HTTP port" 8080
        fi
    fi
    case "$HTTP_PORT" in
        ''|*[!0-9]*) fail "invalid port: $HTTP_PORT" ;;
    esac

    if [ -z "$PUBLIC_URL" ]; then
        if [ -n "$_old_cors" ]; then
            PUBLIC_URL=$_old_cors
        else
            say "where browsers will reach Runix; used for the CORS allow-list"
            ask PUBLIC_URL "public URL" "http://$HOSTNAME_S:$HTTP_PORT"
        fi
    fi

    # Only offered on a first install: on an upgrade the stored password
    # is reused and must not be changed behind the operator's back.
    if [ -z "$ADMIN_PASSWORD" ] && [ "$HAVE_SERVER" -eq 0 ] && [ "$INTERACTIVE" -eq 1 ]; then
        if ! confirm "Generate the initial admin password for me?"; then
            PW_AGAIN=""
            while :; do
                ask_secret ADMIN_PASSWORD "admin password (min 12 chars)"
                ask_secret PW_AGAIN "repeat it"
                if [ "$ADMIN_PASSWORD" != "$PW_AGAIN" ]; then
                    echo "    they do not match"
                elif [ "${#ADMIN_PASSWORD}" -lt 12 ]; then
                    echo "    too short"
                else
                    break
                fi
            done
        fi
    fi
fi

if [ "$WANT_AGENT" -eq 1 ] && [ "$WANT_SERVER" -eq 0 ]; then
    [ -n "$SERVER_URL" ] || SERVER_URL=$(read_env "$AGENT_ENV" RUNIX_AGENT_SERVER_URL)
    [ -n "$TOKEN" ] || TOKEN=$(read_env "$AGENT_ENV" RUNIX_AGENT_TOKEN)

    if [ -z "$SERVER_URL" ] || [ -z "$TOKEN" ]; then
        head2 "Join a control plane"
    fi
    if [ -z "$SERVER_URL" ]; then
        [ "$INTERACTIVE" -eq 1 ] || fail "the control-plane url is required (pass --url)"
        ask SERVER_URL "control-plane URL" ""
    fi
    [ -n "$SERVER_URL" ] || fail "the control-plane url is required"
    case "$SERVER_URL" in
        http://*|https://*|ws://*|wss://*) ;;
        *) fail "the URL must start with http(s):// or ws(s)://" ;;
    esac
    if [ -z "$TOKEN" ]; then
        [ "$INTERACTIVE" -eq 1 ] || fail "an enrollment token is required (pass --token)"
        say "from the UI: Servers → Add server"
        ask TOKEN "enrollment token" ""
    fi
    [ -n "$TOKEN" ] || fail "an enrollment token is required"
fi

# An already-supervised daemon tree must keep its path, so an existing
# data directory always wins over the new default.
if [ "$WANT_AGENT" -eq 1 ]; then
    [ -n "$DATA_DIR" ] || DATA_DIR=$(read_env "$AGENT_ENV" RUNIX_AGENT_DATA_DIR)
    [ -n "$DATA_DIR" ] || DATA_DIR="$PREFIX/agent"
fi

# ------------------------------------------------------------------ summary

head2 "Ready to install"
echo "  install root    $PREFIX"
case "$ROLE" in
    all-in-one) echo "  role            control plane + agent" ;;
    server)     echo "  role            control plane" ;;
    agent)      echo "  role            agent" ;;
esac
if [ "$WANT_SERVER" -eq 1 ]; then
    if [ "$DB_MODE" = docker ]; then
        echo "  database        PostgreSQL in Docker, 127.0.0.1:$PG_PORT"
    else
        echo "  database        existing server"
    fi
    echo "  listen          :$HTTP_PORT"
    echo "  public URL      $PUBLIC_URL"
    if [ "$HAVE_SERVER" -eq 1 ]; then
        echo "  admin password  unchanged"
    elif [ -n "$ADMIN_PASSWORD" ]; then
        echo "  admin password  (the one you entered)"
    else
        echo "  admin password  generated, shown at the end"
    fi
fi
if [ "$WANT_AGENT" -eq 1 ]; then
    [ "$WANT_SERVER" -eq 0 ] && echo "  control plane   $SERVER_URL"
    echo "  agent state     $DATA_DIR"
fi
echo
confirm "Proceed?" || { echo "  cancelled"; exit 0; }

# ------------------------------------------------------------------ download

WORKDIR=$(mktemp -d)
# shellcheck disable=SC2064 # expand WORKDIR now, not at trap time
trap "rm -rf '$WORKDIR'" EXIT

# fetch URL DEST [ACCEPT] — curl or wget, carrying the token when set.
# Returns non-zero on failure rather than exiting, so callers can decide.
fetch() {
    _url=$1; _dest=$2; _accept=${3:-}
    if command -v curl >/dev/null 2>&1; then
        set -- -fsSL -o "$_dest"
        if [ -n "$GITHUB_TOKEN" ]; then
            set -- "$@" -H "Authorization: Bearer $GITHUB_TOKEN"
        fi
        if [ -n "$_accept" ]; then
            set -- "$@" -H "Accept: $_accept"
        fi
        curl "$@" "$_url"
    else
        set -- -qO "$_dest"
        if [ -n "$GITHUB_TOKEN" ]; then
            set -- "$@" --header="Authorization: Bearer $GITHUB_TOKEN"
        fi
        if [ -n "$_accept" ]; then
            set -- "$@" --header="Accept: $_accept"
        fi
        wget "$@" "$_url"
    fi
}

release_api() {
    if [ "$VERSION" = latest ]; then
        echo "https://api.github.com/repos/$REPO/releases/latest"
    else
        echo "https://api.github.com/repos/$REPO/releases/tags/$VERSION"
    fi
}

# asset_id NAME — the numeric id of a release asset, or empty. Only needed
# for private repositories, whose assets are downloadable solely through
# the API. Splitting on '{' puts each asset's id and name on one line
# (GitHub emits "name" before the nested "uploader" object), which avoids
# depending on jq being installed.
asset_id() {
    _name=$1
    _meta=$(mktemp)
    if ! fetch "$(release_api)" "$_meta" "application/vnd.github+json"; then
        rm -f "$_meta"
        return 1
    fi
    if command -v jq >/dev/null 2>&1; then
        _id=$(jq -r --arg n "$_name" '.assets[] | select(.name==$n) | .id' < "$_meta" | head -n1)
    else
        _id=$(tr '{' '\n' < "$_meta" \
            | grep "\"name\": *\"$_name\"" \
            | head -n1 \
            | sed -n 's/.*"id": *\([0-9][0-9]*\).*/\1/p')
    fi
    rm -f "$_meta"
    [ -n "$_id" ] || return 1
    echo "$_id"
}

# try_download NAME DEST — quiet, returns non-zero if the asset is absent.
try_download() {
    _name=$1; _dest=$2
    if [ -n "$GITHUB_TOKEN" ]; then
        _id=$(asset_id "$_name") || return 1
        fetch "https://api.github.com/repos/$REPO/releases/assets/$_id" \
            "$_dest" "application/octet-stream" || return 1
    elif [ "$VERSION" = latest ]; then
        fetch "$DOWNLOAD_BASE/latest/download/$_name" "$_dest" || return 1
    else
        fetch "$DOWNLOAD_BASE/download/$VERSION/$_name" "$_dest" || return 1
    fi
}

SUMS_FETCHED=0
verify_checksum() {
    _file=$1; _name=$2
    if ! command -v sha256sum >/dev/null 2>&1; then
        warn "sha256sum not available; skipping checksum verification"
        return 0
    fi
    if [ "$SUMS_FETCHED" -eq 0 ]; then
        SUMS_FETCHED=1
        try_download SHA256SUMS "$WORKDIR/SHA256SUMS" \
            || warn "release publishes no SHA256SUMS; skipping verification"
    fi
    [ -f "$WORKDIR/SHA256SUMS" ] || return 0
    # The release file lists names as "./<asset>"; accept either form.
    _want=$(sed -n "s|^\([0-9a-f]\{64\}\)  \.\{0,1\}/\{0,1\}$_name\$|\1|p" \
        "$WORKDIR/SHA256SUMS" | head -n1)
    if [ -z "$_want" ]; then
        warn "no checksum listed for $_name; skipping verification"
        return 0
    fi
    _got=$(sha256sum "$_file" | cut -d' ' -f1)
    [ "$_want" = "$_got" ] || fail "checksum mismatch for $_name
  expected $_want
  got      $_got"
    ok "$_name verified"
}

# install_binary NAME LOCAL_PATH
install_binary() {
    _bin=$1; _local=${2:-}
    if [ -n "$_local" ]; then
        [ -f "$_local" ] || fail "binary not found: $_local"
        cp "$_local" "$WORKDIR/$_bin"
        ok "installing $_local"
    else
        _asset="${_bin}_linux_${ARCH}"
        say "downloading $_asset"
        if ! try_download "$_asset" "$WORKDIR/$_bin"; then
            fail "could not download $_asset from $REPO ($VERSION)
  a private repository needs --github-token; otherwise check that the
  release exists, or pass --server-binary / --agent-binary"
        fi
        verify_checksum "$WORKDIR/$_bin" "$_asset"
    fi
    chmod 0755 "$WORKDIR/$_bin"
    mkdir -p "$BIN_DIR"
    # Replace via rename so a running binary is never written in place
    # (that would fail with ETXTBSY).
    mv "$WORKDIR/$_bin" "$BIN_DIR/$_bin.new"
    mv "$BIN_DIR/$_bin.new" "$BIN_DIR/$_bin"
    ok "installed $BIN_DIR/$_bin"
}

# ------------------------------------------------------------------ postgres

provision_postgres() {
    docker info >/dev/null 2>&1 || fail "the docker daemon is not reachable (is it running?)"
    mkdir -p "$PG_DIR/data"
    chmod 0700 "$PG_DIR"

    # Keep the password across re-runs: the existing data directory would
    # not accept a new one.
    PG_PASSWORD=$(read_env "$PG_DIR/.env" POSTGRES_PASSWORD)
    if [ -n "$PG_PASSWORD" ]; then
        say "reusing the existing database password"
    else
        PG_PASSWORD=$(random_secret)
    fi

if [ ! -f "$PG_DIR/.env" ]; then
    umask 077
    cat > "$PG_DIR/.env" <<EOF
# Written by install.sh. Contains the database password: keep 0600.
POSTGRES_DB=$PG_DB
POSTGRES_USER=$PG_USER
POSTGRES_PASSWORD=$PG_PASSWORD
POSTGRES_IMAGE=$PG_IMAGE
POSTGRES_PORT=$PG_PORT
POSTGRES_CONTAINER=$PG_CONTAINER
EOF
    chmod 0600 "$PG_DIR/.env"
    ok "created $PG_DIR/.env"
else
    ok "keeping existing $PG_DIR/.env"
fi

    # Quoted heredoc: ${...} must reach the file, not be expanded here.
    cat > "$PG_DIR/docker-compose.yml" <<'EOF'
# Written by install.sh — re-running the installer rewrites this file, so
# keep customisations in .env beside it where possible.
services:
  postgres:
    image: ${POSTGRES_IMAGE}
    container_name: ${POSTGRES_CONTAINER}
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      # One level down, so a lost+found on a mounted volume cannot make
      # initdb refuse to start.
      PGDATA: /var/lib/postgresql/data/pgdata
    # Bound to loopback: the control plane is the only client, and an
    # internet-exposed database is how these installs get breached.
    ports:
      - "127.0.0.1:${POSTGRES_PORT}:5432"
    volumes:
      - ./data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 20
      start_period: 10s
EOF

    say "starting PostgreSQL ($PG_IMAGE) on 127.0.0.1:$PG_PORT"
    ( cd "$PG_DIR" && $COMPOSE up -d ) || fail "could not start the PostgreSQL container"

    say "waiting for the database to accept connections"
    _i=0
    while [ "$_i" -lt 60 ]; do
        if docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
            ok "database ready"
            break
        fi
        _i=$((_i + 1))
        sleep 2
    done
    if [ "$_i" -ge 60 ]; then
        echo "[runix] the database did not become ready; recent container logs:" >&2
        ( cd "$PG_DIR" && $COMPOSE logs --tail 30 ) >&2 || true
        fail "PostgreSQL did not start"
    fi

    DSN="postgres://$PG_USER:$PG_PASSWORD@127.0.0.1:$PG_PORT/$PG_DB?sslmode=disable"
}

# -------------------------------------------------------------- control plane

GENERATED_PASSWORD=""

install_server() {
    head2 "Installing the control plane"

    if ! id "$SERVER_USER" >/dev/null 2>&1; then
        useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVER_USER" \
            || fail "could not create user $SERVER_USER"
        ok "created service user $SERVER_USER"
    fi

    mkdir -p "$PREFIX"
    install_binary runix-server "$SERVER_BIN"

    [ "$DB_MODE" = docker ] && provision_postgres
    [ -n "$DSN" ] || fail "no database configured"

    mkdir -p "$CONFIG_DIR"
    umask 077

    # Preserve secrets across upgrades: rotating them logs everyone out
    # and orphans encrypted TOTP secrets.
    _jwt=$(read_env "$SERVER_ENV" RUNIX_JWT_SECRET)
    _enc=$(read_env "$SERVER_ENV" RUNIX_ENCRYPTION_KEY)
    [ -n "$_jwt" ] || _jwt=$(random_secret)
    [ -n "$_enc" ] || _enc=$(random_secret)

    if [ -z "$ADMIN_PASSWORD" ]; then
        ADMIN_PASSWORD=$(read_env "$SERVER_ENV" RUNIX_ADMIN_PASSWORD)
        if [ -z "$ADMIN_PASSWORD" ]; then
            ADMIN_PASSWORD=$(random_secret | cut -c1-20)
            GENERATED_PASSWORD=$ADMIN_PASSWORD
        fi
    fi

    if [ ! -f "$SERVER_ENV" ]; then
        {
            echo "# Written by install.sh. Contains secrets: keep 0600."
            echo "RUNIX_ENV=production"
            echo "RUNIX_HTTP_ADDR=:$HTTP_PORT"
            echo "RUNIX_DATABASE_DSN=$DSN"
            echo "RUNIX_JWT_SECRET=$_jwt"
            echo "RUNIX_ENCRYPTION_KEY=$_enc"
            echo "RUNIX_ADMIN_PASSWORD=$ADMIN_PASSWORD"
            echo "RUNIX_LOG_FORMAT=json"
            if [ -n "$PUBLIC_URL" ]; then
                echo "RUNIX_CORS_ORIGINS=$PUBLIC_URL"
            fi
        } > "$SERVER_ENV"

        chown "$SERVER_USER" "$SERVER_ENV"
        chmod 0600 "$SERVER_ENV"
        ok "created $SERVER_ENV"
    else
        ok "keeping existing $SERVER_ENV"
    fi

    if [ "$HAS_SYSTEMD" -eq 1 ]; then
        # With the database in Docker the unit must wait for the daemon,
        # or the control plane races the container on boot.
        _after="network-online.target"
        [ "$DB_MODE" = docker ] && _after="$_after docker.service"
        cat > /etc/systemd/system/runix-server.service <<EOF
[Unit]
Description=Runix control plane
Documentation=https://github.com/$REPO
After=$_after
Wants=network-online.target

[Service]
Type=simple
User=$SERVER_USER
EnvironmentFile=$SERVER_ENV
ExecStart=$BIN_DIR/runix-server
Restart=always
RestartSec=5
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$CONFIG_DIR

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable runix-server >/dev/null 2>&1 || true
        ok "wrote /etc/systemd/system/runix-server.service"
        if [ "$NO_START" -eq 0 ]; then
            systemctl restart runix-server
            sleep 2
            if systemctl is-active --quiet runix-server; then
                ok "control plane running on :$HTTP_PORT"
            else
                echo "[runix] the service failed to start; recent logs:" >&2
                journalctl -u runix-server -n 20 --no-pager >&2 || true
                fail "the control plane did not start"
            fi
        fi
    fi
}

# --------------------------------------------------------------------- agent

install_agent() {
    head2 "Installing the agent"

    mkdir -p "$PREFIX"
    install_binary runix-agent "$AGENT_BIN"

    mkdir -p "$CONFIG_DIR"
    if [ ! -f "$AGENT_ENV" ]; then
        umask 077
        cat > "$AGENT_ENV" <<EOF
# Written by install.sh. Contains the agent credential: keep 0600.
RUNIX_AGENT_SERVER_URL=$SERVER_URL
RUNIX_AGENT_TOKEN=$TOKEN
RUNIX_AGENT_DATA_DIR=$DATA_DIR
RUNIX_AGENT_LOG_FORMAT=json
EOF

        chmod 0600 "$AGENT_ENV"
        ok "created $AGENT_ENV"
    else
        ok "keeping existing $AGENT_ENV"
    fi
    mkdir -p "$DATA_DIR"
    if [ "$AGENT_USER" != root ]; then
        id "$AGENT_USER" >/dev/null 2>&1 || fail "user does not exist: $AGENT_USER"
        chown -R "$AGENT_USER" "$DATA_DIR" "$CONFIG_DIR"
    fi
    ok "wrote $AGENT_ENV"

    if [ "$HAS_SYSTEMD" -eq 1 ]; then
        cat > /etc/systemd/system/runix-agent.service <<EOF
[Unit]
Description=Runix agent
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$AGENT_USER
EnvironmentFile=$AGENT_ENV
ExecStart=$BIN_DIR/runix-agent
Restart=always
RestartSec=5
# The agent manages host workloads, so it deliberately runs unconfined;
# restrict it here if your deployment does not need every provider.
NoNewPrivileges=no

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable runix-agent >/dev/null 2>&1 || true
        ok "wrote /etc/systemd/system/runix-agent.service"
        if [ "$NO_START" -eq 0 ]; then
            systemctl restart runix-agent
            sleep 1
            if systemctl is-active --quiet runix-agent; then
                ok "agent running"
            else
                echo "[runix] the service failed to start; recent logs:" >&2
                journalctl -u runix-agent -n 20 --no-pager >&2 || true
                fail "the agent did not start"
            fi
        fi
    fi
}

# ---------------------------------------------------------------- enrollment

# api_post PATH JSON_FILE OUT [BEARER] — returns non-zero on any error.
api_post() {
    _p=$1; _body=$2; _out=$3; _bearer=${4:-}
    if command -v curl >/dev/null 2>&1; then
        set -- -fsS -X POST "$_p" -H 'Content-Type: application/json' \
               --data-binary "@$_body" -o "$_out"
        [ -n "$_bearer" ] && set -- "$@" -H "Authorization: Bearer $_bearer"
        curl "$@" >/dev/null 2>&1
    else
        set -- -q -O "$_out" --header='Content-Type: application/json' \
               --post-file="$_body"
        [ -n "$_bearer" ] && set -- "$@" --header="Authorization: Bearer $_bearer"
        wget "$@" "$_p" >/dev/null 2>&1
    fi
}

# For an all-in-one host the agent talks to the control plane over
# loopback, and its enrollment token is minted here so the operator never
# has to copy one by hand.
enroll_self() {
    head2 "Enrolling this host with its own control plane"
    SERVER_URL="http://127.0.0.1:$HTTP_PORT"
    _api="$SERVER_URL/api/v1"
    _existing_token=$(read_env "$AGENT_ENV" RUNIX_AGENT_TOKEN)

    say "waiting for the control plane to answer"
    _i=0
    while [ "$_i" -lt 30 ]; do
        if fetch "$_api/health" "$WORKDIR/health" 2>/dev/null; then
            break
        fi
        _i=$((_i + 1))
        sleep 1
    done

    keep_or_fail() {
        # Fall back to whatever token this host already had. It may be
        # stale, in which case the agent will report 401 and the operator
        # can re-pair from the UI — but never silently drop a working one.
        if [ -n "$_existing_token" ]; then
            TOKEN=$_existing_token
            warn "keeping the token already on this host; if the agent cannot"
            warn "connect, rotate it from the UI: Servers → this host → Rotate token"
            return 0
        fi
        return 1
    }

    if [ -z "$ADMIN_PASSWORD" ]; then
        warn "the admin password is unknown, so this host cannot enroll itself"
        keep_or_fail && return 0
        warn "add it from the UI: Servers → Add server"
        return 1
    fi

    printf '{"identifier":"admin","password":"%s"}' "$ADMIN_PASSWORD" > "$WORKDIR/login.json"
    _access=""
    if api_post "$_api/auth/login" "$WORKDIR/login.json" "$WORKDIR/login.out"; then
        _access=$(tr ',' '\n' < "$WORKDIR/login.out" \
            | sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p' | head -n1)
    fi
    if [ -z "$_access" ]; then
        warn "could not sign in to the control plane; skipping auto-enrollment"
        keep_or_fail && return 0
        warn "add this host from the UI: Servers → Add server"
        return 1
    fi

    # A token already in agent.env proves nothing on its own: it may
    # belong to a control plane this host no longer talks to, or to a
    # database that has since been recreated. So look this host up on
    # *this* control plane and decide from what is actually there.
    # Not fetch(): that helper carries the GitHub token, not the API one.
    _sid=""
    if command -v curl >/dev/null 2>&1; then
        curl -fsS -H "Authorization: Bearer $_access" "$_api/servers" \
            -o "$WORKDIR/servers.out" >/dev/null 2>&1 || true
    else
        wget -q -O "$WORKDIR/servers.out" \
            --header="Authorization: Bearer $_access" "$_api/servers" >/dev/null 2>&1 || true
    fi
    if [ -f "$WORKDIR/servers.out" ]; then
        # Each server object becomes one line, with "id" ahead of "name".
        _sid=$(tr '{' '\n' < "$WORKDIR/servers.out" \
            | grep "\"name\": *\"$HOSTNAME_S\"" \
            | head -n1 \
            | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p')
    fi

    # Nothing to do when this host is already paired with this control
    # plane: re-minting a working credential on every upgrade would churn
    # it for no reason. "Already paired" means a record exists here *and*
    # the agent is configured to talk to this control plane — the stale
    # case that motivated all this is precisely a token pointing
    # somewhere else.
    if [ -n "$_sid" ] && [ -n "$_existing_token" ] \
       && [ "$(read_env "$AGENT_ENV" RUNIX_AGENT_SERVER_URL)" = "$SERVER_URL" ]; then
        TOKEN=$_existing_token
        ok "this host is already paired with this control plane — keeping its token"
        return 0
    fi

    TOKEN="rnx_agt_$(random_secret)"
    if [ -n "$_sid" ]; then
        printf '{"agentToken":"%s"}' "$TOKEN" > "$WORKDIR/rotate.json"
        if api_post "$_api/servers/$_sid/token/rotate" "$WORKDIR/rotate.json" \
                    "$WORKDIR/rotate.out" "$_access"; then
            ok "re-paired the existing \"$HOSTNAME_S\" record with a fresh token"
            return 0
        fi
        warn "could not rotate the token for \"$HOSTNAME_S\""
        keep_or_fail && return 0
        return 1
    fi

    printf '{"name":"%s","address":"127.0.0.1","description":"Installed by install.sh","agentToken":"%s"}' \
        "$HOSTNAME_S" "$TOKEN" > "$WORKDIR/server.json"
    if api_post "$_api/servers" "$WORKDIR/server.json" "$WORKDIR/server.out" "$_access"; then
        ok "registered \"$HOSTNAME_S\" and minted its enrollment token"
        return 0
    fi
    warn "could not register this host automatically"
    keep_or_fail && return 0
    warn "add it from the UI: Servers → Add server"
    return 1
}

# --------------------------------------------------------------------- run

[ "$WANT_SERVER" -eq 1 ] && install_server

if [ "$ROLE" = all-in-one ] && [ "$NO_START" -eq 0 ]; then
    enroll_self || WANT_AGENT=0
elif [ "$ROLE" = all-in-one ]; then
    warn "--no-start given, so this host was not enrolled; add it from the UI"
    WANT_AGENT=0
fi

[ "$WANT_AGENT" -eq 1 ] && install_agent

# -------------------------------------------------------------------- done

head2 "Done"
if [ "$WANT_SERVER" -eq 1 ]; then
    echo "  UI / API        $PUBLIC_URL"
    echo "  username        admin"
    if [ -n "$GENERATED_PASSWORD" ]; then
        echo "  password        ${C_B}$GENERATED_PASSWORD${C_0}   ${C_DIM}(change at first login)${C_0}"
    elif [ "$HAVE_SERVER" -eq 1 ]; then
        echo "  password        unchanged"
    else
        echo "  password        the one you entered"
    fi
    echo "  config          $SERVER_ENV"
    [ "$DB_MODE" = docker ] && echo "  database        $PG_DIR"
fi
if [ "$WANT_AGENT" -eq 1 ]; then
    echo "  agent config    $AGENT_ENV"
    echo "  agent state     $DATA_DIR"
fi
if [ "$HAS_SYSTEMD" -eq 1 ]; then
    if [ "$NO_START" -eq 1 ]; then
        echo
        say "installed but not started: systemctl start runix-server runix-agent"
    else
        echo
        say "systemctl status runix-server runix-agent"
    fi
else
    echo
    say "no systemd here; run the binaries yourself, for example:"
    [ "$WANT_SERVER" -eq 1 ] && say "  env \$(grep -v '^#' $SERVER_ENV | xargs) $BIN_DIR/runix-server"
    [ "$WANT_AGENT" -eq 1 ] && say "  env \$(grep -v '^#' $AGENT_ENV | xargs) $BIN_DIR/runix-agent"
fi
if [ "$ROLE" = server ]; then
    echo
    echo "  Add hosts from the UI (Servers → Add server), then run on each:"
    say "  curl -fsSL $DOWNLOAD_BASE/latest/download/install.sh | sudo sh -s -- \\"
    say "      --role agent --url $PUBLIC_URL --token <token>"
fi
echo

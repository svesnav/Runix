#!/bin/sh
# Runix universal installer — the one command that sets a host up.
#
#   curl -fsSL https://github.com/svesnav/Runix/releases/latest/download/install.sh | sudo sh
#
# Asks what this host should be (control plane, agent, or both), checks
# the prerequisites, and hands the answers to install-server.sh /
# install-agent.sh, which do the actual work. Those two stay usable on
# their own for unattended installs; this script is the friendly front
# end, not a second implementation.
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
LOCAL_SERVER_BIN=""
LOCAL_AGENT_BIN=""

# Control plane answers.
DB_MODE=""            # docker | existing
DSN=""
PG_PORT=""
HTTP_PORT=""
PUBLIC_URL=""
ADMIN_PASSWORD=""

# Agent answers.
SERVER_URL=""
TOKEN=""

usage() {
    cat <<EOF
Runix installer

Run with no arguments to be asked what you want. Every answer also has a
flag, so the same script works unattended.

Options:
  --role ROLE          all-in-one | server | agent
  --db MODE            docker | existing        (server roles)
  --dsn DSN            PostgreSQL DSN           (implies --db existing)
  --pg-port PORT       Port for the provisioned database (default 5432)
  --port PORT          Control-plane HTTP port (default 8080)
  --public-url URL     Where browsers reach the UI (sets CORS)
  --admin-password PW  Initial admin password (default: generated)
  --url URL            Control-plane URL        (agent role)
  --token TOKEN        Enrollment token         (agent role)
  --server-binary PATH Install a local control-plane build
  --agent-binary PATH  Install a local agent build
  --version VERSION    Release to install (default: latest)
  --repo OWNER/NAME    Source repository (default: $REPO)
  --github-token TOK   Token for a private repository
  --prefix PATH        Install root (default: $PREFIX)
  -y, --yes            Do not ask for confirmation
  -h, --help           Show this help
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --role) ROLE="$2"; shift 2 ;;
        --db) DB_MODE="$2"; shift 2 ;;
        --dsn) DSN="$2"; DB_MODE=existing; shift 2 ;;
        --pg-port) PG_PORT="$2"; shift 2 ;;
        --port) HTTP_PORT="$2"; shift 2 ;;
        --public-url) PUBLIC_URL="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --url) SERVER_URL="$2"; shift 2 ;;
        --token) TOKEN="$2"; shift 2 ;;
        --server-binary) LOCAL_SERVER_BIN="$2"; shift 2 ;;
        --agent-binary) LOCAL_AGENT_BIN="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --repo) REPO="$2"; shift 2 ;;
        --github-token) GITHUB_TOKEN="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        -y|--yes) ASSUME_YES=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[ -n "$DOWNLOAD_BASE" ] || DOWNLOAD_BASE="https://github.com/$REPO/releases"

# ------------------------------------------------------------------ output

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    C_B=$(printf '\033[1m'); C_DIM=$(printf '\033[2m'); C_OK=$(printf '\033[32m')
    C_WARN=$(printf '\033[33m'); C_ERR=$(printf '\033[31m'); C_0=$(printf '\033[0m')
else
    C_B=""; C_DIM=""; C_OK=""; C_WARN=""; C_ERR=""; C_0=""
fi

say()  { echo "${C_DIM}[runix]${C_0} $*"; }
ok()   { echo "  ${C_OK}✓${C_0} $*"; }
warn() { echo "  ${C_WARN}!${C_0} $*"; }
fail() { echo "${C_ERR}[runix] error:${C_0} $*" >&2; exit 1; }
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

need() {
    # need VALUE "what it is" "which flag"
    [ -n "$1" ] || fail "$2 is required in non-interactive mode (pass $3)"
}

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
        [ -n "$_old" ] && stty "$_old" 2>/dev/null || stty echo 2>/dev/null || true
        echo
    else
        read -r _ans || _ans=""
    fi
    eval "$_var=\$_ans"
}

confirm() {
    # confirm "question" — default yes
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
    echo "  $_q"
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
            ''|*[!0-9]*) echo "    enter a number" ; continue ;;
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
HAS_COMPOSE=0
if [ "$HAS_DOCKER" -eq 1 ]; then
    if docker compose version >/dev/null 2>&1 || command -v docker-compose >/dev/null 2>&1; then
        HAS_COMPOSE=1
    fi
fi

echo
echo "${C_B}Runix installer${C_0}  ${C_DIM}($REPO, $VERSION)${C_0}"
echo "${C_DIM}────────────────────────────────────────────────${C_0}"
echo "  host          $HOSTNAME_S  (linux/$ARCH)"
if [ "$HAS_SYSTEMD" -eq 1 ]; then ok "systemd"; else warn "no systemd — services must be started by hand"; fi
if [ "$HAS_DOCKER" -eq 1 ]; then
    if [ "$HAS_COMPOSE" -eq 1 ]; then ok "docker + compose"; else warn "docker without compose plugin"; fi
else
    warn "no docker — cannot provision PostgreSQL, and Docker runtimes will be unavailable"
fi
if [ -e "$PREFIX/bin/runix-server" ] || [ -e "$PREFIX/bin/runix-agent" ]; then
    ok "existing install at $PREFIX (this will upgrade it, keeping config)"
fi

# ---------------------------------------------------------------- questions

head2 "What should this host run?"
if [ -z "$ROLE" ]; then
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
    head2 "Database"
    if [ -z "$DB_MODE" ]; then
        if [ "$INTERACTIVE" -eq 0 ]; then
            DB_MODE=docker
        elif [ "$HAS_COMPOSE" -eq 1 ]; then
            choose DB_MODE "" \
                "docker:Run PostgreSQL for me, in Docker Compose (recommended)" \
                "existing:Use a PostgreSQL server I already have"
        else
            warn "docker compose is unavailable, so PostgreSQL cannot be provisioned here"
            DB_MODE=existing
        fi
    fi

    if [ "$DB_MODE" = docker ]; then
        [ "$HAS_COMPOSE" -eq 1 ] || fail "docker compose is required to provision PostgreSQL
  install it (https://docs.docker.com/engine/install/), or choose an existing database"
        [ -n "$PG_PORT" ] || PG_PORT=5432
        if port_busy "$PG_PORT"; then
            warn "port $PG_PORT is already in use on this host"
            ask PG_PORT "port for the Runix database" 5433
        fi
    else
        if [ -z "$DSN" ]; then
            [ "$INTERACTIVE" -eq 1 ] || need "$DSN" "a database DSN" "--dsn"
            echo "  ${C_DIM}example: postgres://runix:secret@127.0.0.1:5432/runix?sslmode=disable${C_0}"
            ask DSN "PostgreSQL DSN" ""
        fi
        [ -n "$DSN" ] || fail "a DSN is required when not provisioning PostgreSQL"
        case "$DSN" in
            postgres://*|postgresql://*) ;;
            *) fail "that does not look like a PostgreSQL DSN: $DSN" ;;
        esac
    fi

    head2 "Control plane"
    if [ -z "$HTTP_PORT" ]; then
        ask HTTP_PORT "HTTP port" 8080
    fi
    case "$HTTP_PORT" in
        ''|*[!0-9]*) fail "invalid port: $HTTP_PORT" ;;
    esac
    if port_busy "$HTTP_PORT"; then
        warn "port $HTTP_PORT is already in use — the service will fail to bind"
    fi

    if [ -z "$PUBLIC_URL" ]; then
        echo "  ${C_DIM}where browsers will reach Runix; used for the CORS allow-list${C_0}"
        ask PUBLIC_URL "public URL" "http://$HOSTNAME_S:$HTTP_PORT"
    fi

    if [ -z "$ADMIN_PASSWORD" ] && [ "$INTERACTIVE" -eq 1 ]; then
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
    head2 "Join a control plane"
    if [ -z "$SERVER_URL" ]; then
        [ "$INTERACTIVE" -eq 1 ] || need "$SERVER_URL" "the control-plane URL" "--url"
        ask SERVER_URL "control-plane URL" ""
    fi
    [ -n "$SERVER_URL" ] || fail "the control-plane URL is required"
    case "$SERVER_URL" in
        http://*|https://*|ws://*|wss://*) ;;
        *) fail "the URL must start with http(s):// or ws(s)://" ;;
    esac
    if [ -z "$TOKEN" ]; then
        [ "$INTERACTIVE" -eq 1 ] || need "$TOKEN" "an enrollment token" "--token"
        echo "  ${C_DIM}from the UI: Servers → Add server${C_0}"
        ask TOKEN "enrollment token" ""
    fi
    [ -n "$TOKEN" ] || fail "an enrollment token is required"
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
    if [ -n "$ADMIN_PASSWORD" ]; then
        echo "  admin password  (the one you entered)"
    else
        echo "  admin password  generated, shown at the end"
    fi
fi
if [ "$WANT_AGENT" -eq 1 ] && [ "$WANT_SERVER" -eq 0 ]; then
    echo "  control plane   $SERVER_URL"
fi
echo
confirm "Proceed?" || { echo "  cancelled"; exit 0; }

# ------------------------------------------------- fetch the worker scripts

WORKDIR=$(mktemp -d)
# shellcheck disable=SC2064 # expand WORKDIR now, not at trap time
trap "rm -rf '$WORKDIR'" EXIT

fetch() {
    _url=$1; _dest=$2; _accept=${3:-}
    if command -v curl >/dev/null 2>&1; then
        set -- -fsSL -o "$_dest"
        [ -n "$GITHUB_TOKEN" ] && set -- "$@" -H "Authorization: Bearer $GITHUB_TOKEN"
        [ -n "$_accept" ] && set -- "$@" -H "Accept: $_accept"
        curl "$@" "$_url"
    else
        set -- -qO "$_dest"
        [ -n "$GITHUB_TOKEN" ] && set -- "$@" --header="Authorization: Bearer $GITHUB_TOKEN"
        [ -n "$_accept" ] && set -- "$@" --header="Accept: $_accept"
        wget "$@" "$_url"
    fi
}

# Running from a checkout? Then use the scripts sitting next to this one —
# no download, and local edits are honoured. Piped through `sh`, $0 is
# just "sh" and dirname yields the working directory, so a stray file
# there must not be mistaken for ours: require this script to be present
# alongside them too.
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || echo)
if [ -n "$SCRIPT_DIR" ] && [ ! -f "$SCRIPT_DIR/install.sh" ]; then
    SCRIPT_DIR=""
fi

get_script() {
    _name=$1
    if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/$_name" ]; then
        cp "$SCRIPT_DIR/$_name" "$WORKDIR/$_name"
    else
        _url="$DOWNLOAD_BASE/latest/download/$_name"
        [ "$VERSION" = latest ] || _url="$DOWNLOAD_BASE/download/$VERSION/$_name"
        if [ -n "$GITHUB_TOKEN" ]; then
            # Private releases are only reachable through the API; the
            # component installers know how, so let one of them do it.
            _url=""
        fi
        if [ -n "$_url" ]; then
            fetch "$_url" "$WORKDIR/$_name" || fail "could not download $_name
  a private repository needs --github-token, or run this from a checkout"
        else
            fail "with --github-token, run the installer from a checkout of the repository
  (git clone, then sh scripts/install.sh) — the bootstrap scripts themselves
  cannot be fetched anonymously from a private release"
        fi
    fi
    chmod +x "$WORKDIR/$_name"
}

# ------------------------------------------------------------------ install

pass_through() {
    # Options every component installer understands.
    set -- --prefix "$PREFIX" --version "$VERSION" --repo "$REPO"
    [ -n "$GITHUB_TOKEN" ] && set -- "$@" --github-token "$GITHUB_TOKEN"
    echo "$@"
}

if [ "$WANT_SERVER" -eq 1 ]; then
    head2 "Installing the control plane"
    get_script install-server.sh

    # Built as a positional list so values with spaces survive.
    set -- --prefix "$PREFIX" --version "$VERSION" --repo "$REPO" \
           --addr ":$HTTP_PORT" --cors "$PUBLIC_URL"
    [ -n "$GITHUB_TOKEN" ] && set -- "$@" --github-token "$GITHUB_TOKEN"
    [ -n "$LOCAL_SERVER_BIN" ] && set -- "$@" --binary "$LOCAL_SERVER_BIN"
    [ -n "$ADMIN_PASSWORD" ] && set -- "$@" --admin-password "$ADMIN_PASSWORD"
    if [ "$DB_MODE" = docker ]; then
        set -- "$@" --with-postgres --pg-port "$PG_PORT"
    else
        set -- "$@" --dsn "$DSN"
    fi

    # Capture the output so the generated admin password can be shown once
    # at the end, while still streaming progress to the operator.
    if ! sh "$WORKDIR/install-server.sh" "$@" 2>&1 | tee "$WORKDIR/server.log"; then
        fail "the control-plane install failed (see above)"
    fi
    ADMIN_PASSWORD_SHOWN=$(sed -n 's/.*initial admin password: \([^ ]*\).*/\1/p' "$WORKDIR/server.log" | head -n1)
    [ -n "$ADMIN_PASSWORD" ] || ADMIN_PASSWORD=$ADMIN_PASSWORD_SHOWN
fi

# For an all-in-one host the agent talks to the control plane over
# loopback, and its enrollment token is minted here so the operator never
# has to copy one by hand.
if [ "$ROLE" = all-in-one ]; then
    head2 "Enrolling this host with its own control plane"
    SERVER_URL="http://127.0.0.1:$HTTP_PORT"
    API="$SERVER_URL/api/v1"

    say "waiting for the control plane to answer"
    i=0
    while [ "$i" -lt 30 ]; do
        if fetch "$API/health" "$WORKDIR/health" 2>/dev/null; then
            break
        fi
        i=$((i + 1))
        sleep 1
    done

    TOKEN="rnx_agt_$(random_secret)"
    if [ -z "$ADMIN_PASSWORD" ]; then
        warn "the admin password is unknown, so this host cannot enroll itself"
        warn "add it from the UI: Servers → Add server"
        WANT_AGENT=0
    else
        printf '{"identifier":"admin","password":"%s"}' "$ADMIN_PASSWORD" > "$WORKDIR/login.json"
        _access=""
        if command -v curl >/dev/null 2>&1; then
            curl -fsS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
                --data-binary "@$WORKDIR/login.json" -o "$WORKDIR/login.out" 2>/dev/null || true
        else
            wget -q -O "$WORKDIR/login.out" --header='Content-Type: application/json' \
                --post-file="$WORKDIR/login.json" "$API/auth/login" 2>/dev/null || true
        fi
        if [ -f "$WORKDIR/login.out" ]; then
            _access=$(tr ',' '\n' < "$WORKDIR/login.out" \
                | sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p' | head -n1)
        fi
        if [ -z "$_access" ]; then
            warn "could not sign in to the control plane; skipping auto-enrollment"
            warn "add this host from the UI: Servers → Add server"
            WANT_AGENT=0
        else
            printf '{"name":"%s","address":"127.0.0.1","description":"Installed by install.sh","agentToken":"%s"}' \
                "$HOSTNAME_S" "$TOKEN" > "$WORKDIR/server.json"
            _created=1
            if command -v curl >/dev/null 2>&1; then
                curl -fsS -X POST "$API/servers" -H 'Content-Type: application/json' \
                    -H "Authorization: Bearer $_access" \
                    --data-binary "@$WORKDIR/server.json" -o "$WORKDIR/server.out" 2>/dev/null || _created=0
            else
                wget -q -O "$WORKDIR/server.out" --header='Content-Type: application/json' \
                    --header="Authorization: Bearer $_access" \
                    --post-file="$WORKDIR/server.json" "$API/servers" 2>/dev/null || _created=0
            fi
            if [ "$_created" -eq 1 ]; then
                ok "registered \"$HOSTNAME_S\" and minted its enrollment token"
            else
                warn "could not register this host automatically (a server named"
                warn "\"$HOSTNAME_S\" may already exist); add it from the UI instead"
                WANT_AGENT=0
            fi
        fi
    fi
fi

if [ "$WANT_AGENT" -eq 1 ]; then
    head2 "Installing the agent"
    get_script install-agent.sh
    set -- --prefix "$PREFIX" --version "$VERSION" --repo "$REPO" \
           --url "$SERVER_URL" --token "$TOKEN"
    [ -n "$GITHUB_TOKEN" ] && set -- "$@" --github-token "$GITHUB_TOKEN"
    [ -n "$LOCAL_AGENT_BIN" ] && set -- "$@" --binary "$LOCAL_AGENT_BIN"
    sh "$WORKDIR/install-agent.sh" "$@" || fail "the agent install failed (see above)"
fi

# -------------------------------------------------------------------- done

head2 "Done"
if [ "$WANT_SERVER" -eq 1 ]; then
    echo "  UI / API        $PUBLIC_URL"
    echo "  username        admin"
    if [ -n "${ADMIN_PASSWORD_SHOWN:-}" ]; then
        echo "  password        ${C_B}$ADMIN_PASSWORD_SHOWN${C_0}   ${C_DIM}(change at first login)${C_0}"
    else
        echo "  password        the one you entered"
    fi
    echo "  config          $PREFIX/etc/server.env"
    if [ "$DB_MODE" = docker ]; then
        echo "  database        $PREFIX/postgres"
    fi
fi
if [ "$WANT_AGENT" -eq 1 ]; then
    echo "  agent config    $PREFIX/etc/agent.env"
fi
if [ "$HAS_SYSTEMD" -eq 1 ]; then
    echo
    echo "  ${C_DIM}systemctl status runix-server runix-agent${C_0}"
fi
if [ "$ROLE" = server ]; then
    echo
    echo "  Add hosts from the UI (Servers → Add server), then run on each:"
    echo "  ${C_DIM}curl -fsSL $DOWNLOAD_BASE/latest/download/install.sh | sudo sh -s -- \\"
    echo "      --role agent --url $PUBLIC_URL --token <token>${C_0}"
fi
echo

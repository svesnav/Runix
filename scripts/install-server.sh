#!/bin/sh
# Runix control-plane installer.
#
#   # everything, including a PostgreSQL container:
#   sudo ./install-server.sh
#
#   # against a database you already run:
#   sudo ./install-server.sh --dsn 'postgres://runix:secret@db:5432/runix?sslmode=disable'
#
# Everything lands under /opt/runix (binaries, config, database data), so
# removing the product is one directory plus a unit file.
#
# Generates JWT/encryption secrets on first run and keeps them across
# upgrades — regenerating them would invalidate every session and make
# stored TOTP secrets unreadable.
#
# POSIX sh on purpose: minimal images often have no bash.
set -eu

BIN_NAME=runix-server
PREFIX=${RUNIX_PREFIX:-/opt/runix}
INSTALL_DIR=${RUNIX_INSTALL_DIR:-}
CONFIG_DIR=${RUNIX_CONFIG_DIR:-}
PG_DIR=${RUNIX_POSTGRES_DIR:-}
SERVICE_NAME=runix-server
SERVICE_USER=${RUNIX_SERVER_USER:-runix}

DSN=${RUNIX_DATABASE_DSN:-}
HTTP_ADDR=${RUNIX_HTTP_ADDR:-:8080}
CORS_ORIGINS=${RUNIX_CORS_ORIGINS:-}
ADMIN_PASSWORD=${RUNIX_ADMIN_PASSWORD:-}
VERSION=${RUNIX_VERSION:-latest}
REPO=${RUNIX_REPO:-svesnav/Runix}
DOWNLOAD_BASE=${RUNIX_DOWNLOAD_BASE:-}
# Private repositories serve release assets only through the API, which
# needs a token with `contents:read`.
GITHUB_TOKEN=${RUNIX_GITHUB_TOKEN:-${GITHUB_TOKEN:-}}
LOCAL_BINARY=""
NO_START=0

# PostgreSQL-in-Docker is the default so a bare host works with no
# arguments; passing --dsn (or --no-postgres) opts out.
WITH_POSTGRES=auto
PG_IMAGE=${RUNIX_POSTGRES_IMAGE:-postgres:17-alpine}
PG_PORT=${RUNIX_POSTGRES_PORT:-5432}
PG_DB=${RUNIX_POSTGRES_DB:-runix}
PG_USER=${RUNIX_POSTGRES_USER:-runix}
PG_CONTAINER=${RUNIX_POSTGRES_CONTAINER:-runix-postgres}
PG_PASSWORD=""

usage() {
    cat <<EOF
Runix control-plane installer

By default this provisions PostgreSQL as a Docker Compose service under
\$PREFIX/postgres and points the control plane at it. Pass --dsn to use a
database you already run.

Options:
  --dsn DSN            PostgreSQL DSN (implies --no-postgres)
  --with-postgres      Force provisioning the PostgreSQL container
  --no-postgres        Do not provision PostgreSQL (--dsn then required)
  --pg-port PORT       Host port for the container (default: $PG_PORT)
  --pg-image IMAGE     PostgreSQL image (default: $PG_IMAGE)
  --addr ADDR          HTTP listen address (default: $HTTP_ADDR)
  --cors ORIGINS       Comma-separated browser origins allowed to call the API
  --admin-password PW  Initial admin password (default: generated and logged)
  --binary PATH        Install this local binary instead of downloading
  --version VERSION    Release to download (default: latest)
  --repo OWNER/NAME    GitHub repository to download from (default: $REPO)
  --github-token TOK   Token for downloading from a private repository
  --prefix PATH        Install root (default: $PREFIX)
  --user USER          Service user, created if missing (default: runix)
  --no-start           Install and configure, but do not start
  -h, --help           Show this help

Environment equivalents: RUNIX_PREFIX, RUNIX_DATABASE_DSN, RUNIX_VERSION,
RUNIX_REPO, RUNIX_GITHUB_TOKEN, RUNIX_POSTGRES_PORT, RUNIX_POSTGRES_IMAGE.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --dsn) DSN="$2"; shift 2 ;;
        --with-postgres) WITH_POSTGRES=yes; shift ;;
        --no-postgres) WITH_POSTGRES=no; shift ;;
        --pg-port) PG_PORT="$2"; shift 2 ;;
        --pg-image) PG_IMAGE="$2"; shift 2 ;;
        --addr) HTTP_ADDR="$2"; shift 2 ;;
        --cors) CORS_ORIGINS="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --repo) REPO="$2"; shift 2 ;;
        --github-token) GITHUB_TOKEN="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        --user) SERVICE_USER="$2"; shift 2 ;;
        --no-start) NO_START=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

# Resolved after parsing so --prefix applies to all of them.
[ -n "$INSTALL_DIR" ] || INSTALL_DIR="$PREFIX/bin"
[ -n "$CONFIG_DIR" ] || CONFIG_DIR="$PREFIX/etc"
[ -n "$PG_DIR" ] || PG_DIR="$PREFIX/postgres"
[ -n "$DOWNLOAD_BASE" ] || DOWNLOAD_BASE="https://github.com/$REPO/releases"

log()  { echo "[runix] $*"; }
fail() { echo "[runix] error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root (try: sudo $0 ...)"
[ "$(uname -s)" = "Linux" ] || fail "the server installer supports Linux only"

ENV_FILE="$CONFIG_DIR/server.env"

# Releases before the /opt layout installed to /etc/runix. Carry that file
# forward so an upgrade keeps its secrets instead of logging everyone out.
LEGACY_ENV=/etc/runix/server.env
if [ ! -f "$ENV_FILE" ] && [ -f "$LEGACY_ENV" ]; then
    mkdir -p "$CONFIG_DIR"
    cp "$LEGACY_ENV" "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
    log "migrated existing config from $LEGACY_ENV"
fi

read_existing() {
    if [ -f "$ENV_FILE" ]; then
        sed -n "s/^$1=//p" "$ENV_FILE" | head -n1
    fi
}

if [ "$WITH_POSTGRES" = auto ]; then
    if [ -n "$DSN" ] || [ -n "$(read_existing RUNIX_DATABASE_DSN)" ]; then
        WITH_POSTGRES=no
    else
        WITH_POSTGRES=yes
    fi
fi
if [ -n "$DSN" ]; then
    WITH_POSTGRES=no
fi

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) fail "unsupported architecture: $ARCH" ;;
esac

random_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# ---------------------------------------------------------------- download

# fetch URL DEST [ACCEPT] — curl or wget, carrying the token when set.
# Returns non-zero on failure rather than exiting, so callers can decide.
fetch() {
    _url=$1
    _dest=$2
    _accept=${3:-}
    if command -v curl >/dev/null 2>&1; then
        set -- -fsSL -o "$_dest"
        if [ -n "$GITHUB_TOKEN" ]; then
            set -- "$@" -H "Authorization: Bearer $GITHUB_TOKEN"
        fi
        if [ -n "$_accept" ]; then
            set -- "$@" -H "Accept: $_accept"
        fi
        curl "$@" "$_url"
    elif command -v wget >/dev/null 2>&1; then
        set -- -qO "$_dest"
        if [ -n "$GITHUB_TOKEN" ]; then
            set -- "$@" --header="Authorization: Bearer $GITHUB_TOKEN"
        fi
        if [ -n "$_accept" ]; then
            set -- "$@" --header="Accept: $_accept"
        fi
        wget "$@" "$_url"
    else
        fail "need curl or wget to download (or pass --binary)"
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
    _name=$1
    _dest=$2
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

verify_checksum() {
    _file=$1
    _name=$2
    _sums=$3
    if ! command -v sha256sum >/dev/null 2>&1; then
        log "sha256sum not available; skipping checksum verification"
        return 0
    fi
    # The release file lists names as "./<asset>"; accept either form.
    _want=$(sed -n "s|^\([0-9a-f]\{64\}\)  \.\{0,1\}/\{0,1\}$_name\$|\1|p" "$_sums" | head -n1)
    if [ -z "$_want" ]; then
        log "no checksum listed for $_name; skipping verification"
        return 0
    fi
    _got=$(sha256sum "$_file" | cut -d' ' -f1)
    [ "$_want" = "$_got" ] || fail "checksum mismatch for $_name
  expected $_want
  got      $_got"
    log "checksum verified"
}

install_binary() {
    tmp=$(mktemp -d)
    # shellcheck disable=SC2064 # expand tmp now, not at trap time
    trap "rm -rf '$tmp'" EXIT

    if [ -n "$LOCAL_BINARY" ]; then
        [ -f "$LOCAL_BINARY" ] || fail "binary not found: $LOCAL_BINARY"
        log "installing $LOCAL_BINARY"
        cp "$LOCAL_BINARY" "$tmp/$BIN_NAME"
    else
        asset="${BIN_NAME}_linux_${ARCH}"
        log "downloading $asset ($REPO, $VERSION)"
        if ! try_download "$asset" "$tmp/$BIN_NAME"; then
            fail "could not download $asset from $REPO ($VERSION)
  a private repository needs --github-token; otherwise check that the
  release exists, or pass --binary to install a local build"
        fi
        if try_download SHA256SUMS "$tmp/SHA256SUMS"; then
            verify_checksum "$tmp/$BIN_NAME" "$asset" "$tmp/SHA256SUMS"
        else
            log "release publishes no SHA256SUMS; skipping verification"
        fi
    fi

    chmod 0755 "$tmp/$BIN_NAME"
    mkdir -p "$INSTALL_DIR"
    # Replace via rename so a running binary is never written in place
    # (that would fail with ETXTBSY).
    mv "$tmp/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME.new"
    mv "$INSTALL_DIR/$BIN_NAME.new" "$INSTALL_DIR/$BIN_NAME"
    log "installed $INSTALL_DIR/$BIN_NAME"
}

# ---------------------------------------------------------------- postgres

COMPOSE=""
detect_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE="docker compose"
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE="docker-compose"
    fi
}

provision_postgres() {
    command -v docker >/dev/null 2>&1 || fail "docker is required to provision PostgreSQL
  install it (https://docs.docker.com/engine/install/) and re-run, or pass
  --dsn to point at a database you already have"
    docker info >/dev/null 2>&1 || fail "the docker daemon is not reachable (is it running?)"
    detect_compose
    [ -n "$COMPOSE" ] || fail "docker compose is required
  install the compose plugin (docker-compose-plugin), or pass --dsn"

    mkdir -p "$PG_DIR/data"
    chmod 0700 "$PG_DIR"

    # Keep the password across re-runs: the existing data directory would
    # not accept a new one.
    pg_env="$PG_DIR/.env"
    if [ -f "$pg_env" ]; then
        PG_PASSWORD=$(sed -n 's/^POSTGRES_PASSWORD=//p' "$pg_env" | head -n1)
        if [ -n "$PG_PASSWORD" ]; then
            log "reusing the existing database password"
        fi
    fi
    [ -n "$PG_PASSWORD" ] || PG_PASSWORD=$(random_secret)

    umask 077
    cat > "$pg_env" <<EOF
# Written by install-server.sh. Contains the database password: keep 0600.
POSTGRES_DB=$PG_DB
POSTGRES_USER=$PG_USER
POSTGRES_PASSWORD=$PG_PASSWORD
POSTGRES_IMAGE=$PG_IMAGE
POSTGRES_PORT=$PG_PORT
POSTGRES_CONTAINER=$PG_CONTAINER
EOF
    chmod 0600 "$pg_env"

    # Quoted heredoc: ${...} must reach the file, not be expanded here.
    cat > "$PG_DIR/docker-compose.yml" <<'EOF'
# Written by install-server.sh — re-running the installer rewrites this
# file, so keep customisations in .env beside it where possible.
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

    log "starting PostgreSQL ($PG_IMAGE) on 127.0.0.1:$PG_PORT"
    ( cd "$PG_DIR" && $COMPOSE up -d ) || fail "could not start the PostgreSQL container"

    log "waiting for the database to accept connections"
    i=0
    while [ "$i" -lt 60 ]; do
        if docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
            log "database ready"
            break
        fi
        i=$((i + 1))
        sleep 2
    done
    if [ "$i" -ge 60 ]; then
        echo "[runix] the database did not become ready; recent container logs:" >&2
        ( cd "$PG_DIR" && $COMPOSE logs --tail 30 ) >&2 || true
        fail "PostgreSQL did not start"
    fi

    DSN="postgres://$PG_USER:$PG_PASSWORD@127.0.0.1:$PG_PORT/$PG_DB?sslmode=disable"
}

# ------------------------------------------------------------------ config

ensure_user() {
    if ! id "$SERVICE_USER" >/dev/null 2>&1; then
        useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" \
            || fail "could not create user $SERVICE_USER"
        log "created service user $SERVICE_USER"
    fi
}

write_config() {
    mkdir -p "$CONFIG_DIR"
    umask 077

    # Preserve secrets across upgrades: rotating them logs everyone out and
    # orphans encrypted TOTP secrets.
    jwt=$(read_existing RUNIX_JWT_SECRET)
    enc=$(read_existing RUNIX_ENCRYPTION_KEY)
    [ -n "$jwt" ] || jwt=$(random_secret)
    [ -n "$enc" ] || enc=$(random_secret)

    generated_password=""
    if [ -z "$ADMIN_PASSWORD" ]; then
        existing_admin=$(read_existing RUNIX_ADMIN_PASSWORD)
        if [ -n "$existing_admin" ]; then
            ADMIN_PASSWORD="$existing_admin"
        else
            ADMIN_PASSWORD=$(random_secret | cut -c1-20)
            generated_password="$ADMIN_PASSWORD"
        fi
    fi

    {
        echo "# Written by install-server.sh. Contains secrets: keep 0600."
        echo "RUNIX_ENV=production"
        echo "RUNIX_HTTP_ADDR=$HTTP_ADDR"
        echo "RUNIX_DATABASE_DSN=$DSN"
        echo "RUNIX_JWT_SECRET=$jwt"
        echo "RUNIX_ENCRYPTION_KEY=$enc"
        echo "RUNIX_ADMIN_PASSWORD=$ADMIN_PASSWORD"
        echo "RUNIX_LOG_FORMAT=json"
        if [ -n "$CORS_ORIGINS" ]; then
            echo "RUNIX_CORS_ORIGINS=$CORS_ORIGINS"
        fi
    } > "$ENV_FILE"
    chown "$SERVICE_USER" "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
    log "wrote $ENV_FILE"

    if [ -n "$generated_password" ]; then
        log "initial admin password: $generated_password  (username: admin)"
        log "you will be asked to change it at first login"
    fi
}

write_service() {
    # With the database in Docker the unit must wait for the daemon, or the
    # control plane races the container on boot.
    after="network-online.target"
    if [ "$WITH_POSTGRES" = yes ]; then
        after="$after docker.service"
    fi

    cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=Runix control plane
Documentation=https://github.com/$REPO
After=$after
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_DIR/$BIN_NAME
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
    log "wrote /etc/systemd/system/$SERVICE_NAME.service"
}

ensure_user
mkdir -p "$PREFIX"
install_binary

if [ "$WITH_POSTGRES" = yes ]; then
    provision_postgres
else
    [ -n "$DSN" ] || DSN=$(read_existing RUNIX_DATABASE_DSN)
    [ -n "$DSN" ] || fail "no database configured: pass --dsn, or drop --no-postgres to provision one"
fi

write_config

if [ -d /run/systemd/system ]; then
    write_service
    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
    if [ "$NO_START" -eq 1 ]; then
        log "installed; start it with: systemctl start $SERVICE_NAME"
    else
        systemctl restart "$SERVICE_NAME"
        sleep 2
        if systemctl is-active --quiet "$SERVICE_NAME"; then
            log "control plane running on $HTTP_ADDR"
        else
            echo "[runix] the service failed to start; recent logs:" >&2
            journalctl -u "$SERVICE_NAME" -n 20 --no-pager >&2 || true
            exit 1
        fi
    fi
else
    log "systemd not detected; run it yourself:"
    log "  env \$(grep -v '^#' $ENV_FILE | xargs) $INSTALL_DIR/$BIN_NAME"
fi

log "install root: $PREFIX"
if [ "$WITH_POSTGRES" = yes ]; then
    log "database: $PG_DIR  (manage with: cd $PG_DIR && $COMPOSE ps)"
fi

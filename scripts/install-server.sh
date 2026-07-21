#!/bin/sh
# Runix control-plane installer.
#
#   ./install-server.sh --binary ./runix-server \
#       --dsn 'postgres://runix:secret@127.0.0.1:5432/runix?sslmode=disable'
#
# Generates JWT/encryption secrets on first run and keeps them across
# upgrades — regenerating them would invalidate every session and make
# stored TOTP secrets unreadable.
set -eu

BIN_NAME=runix-server
INSTALL_DIR=${RUNIX_INSTALL_DIR:-/usr/local/bin}
CONFIG_DIR=${RUNIX_CONFIG_DIR:-/etc/runix}
SERVICE_NAME=runix-server
SERVICE_USER=${RUNIX_SERVER_USER:-runix}

DSN=${RUNIX_DATABASE_DSN:-}
HTTP_ADDR=${RUNIX_HTTP_ADDR:-:8080}
CORS_ORIGINS=${RUNIX_CORS_ORIGINS:-}
ADMIN_PASSWORD=${RUNIX_ADMIN_PASSWORD:-}
VERSION=${RUNIX_VERSION:-latest}
DOWNLOAD_BASE=${RUNIX_DOWNLOAD_BASE:-https://github.com/runix/runix/releases}
LOCAL_BINARY=""
NO_START=0

usage() {
    cat <<EOF
Runix control-plane installer

Options:
  --dsn DSN            PostgreSQL DSN (required on first install)
  --addr ADDR          HTTP listen address (default: $HTTP_ADDR)
  --cors ORIGINS       Comma-separated browser origins allowed to call the API
  --admin-password PW  Initial admin password (default: generated and logged)
  --binary PATH        Install this local binary instead of downloading
  --version VERSION    Release to download (default: latest)
  --user USER          Service user, created if missing (default: runix)
  --no-start           Install and configure, but do not start
  -h, --help           Show this help
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --dsn) DSN="$2"; shift 2 ;;
        --addr) HTTP_ADDR="$2"; shift 2 ;;
        --cors) CORS_ORIGINS="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --user) SERVICE_USER="$2"; shift 2 ;;
        --no-start) NO_START=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

log()  { echo "[runix] $*"; }
fail() { echo "[runix] error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root (try: sudo $0 ...)"
[ "$(uname -s)" = "Linux" ] || fail "the server installer supports Linux only"

ENV_FILE="$CONFIG_DIR/server.env"
read_existing() { [ -f "$ENV_FILE" ] && sed -n "s/^$1=//p" "$ENV_FILE" | head -n1 || true; }

[ -n "$DSN" ] || DSN=$(read_existing RUNIX_DATABASE_DSN)
[ -n "$DSN" ] || fail "--dsn is required (PostgreSQL connection string)"

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

install_binary() {
    tmp=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp'" EXIT

    if [ -n "$LOCAL_BINARY" ]; then
        [ -f "$LOCAL_BINARY" ] || fail "binary not found: $LOCAL_BINARY"
        log "installing $LOCAL_BINARY"
        cp "$LOCAL_BINARY" "$tmp/$BIN_NAME"
    else
        if [ "$VERSION" = "latest" ]; then
            url="$DOWNLOAD_BASE/latest/download/${BIN_NAME}_linux_${ARCH}"
        else
            url="$DOWNLOAD_BASE/download/$VERSION/${BIN_NAME}_linux_${ARCH}"
        fi
        log "downloading $url"
        if command -v curl >/dev/null 2>&1; then
            curl -fsSL "$url" -o "$tmp/$BIN_NAME" || fail "download failed"
        elif command -v wget >/dev/null 2>&1; then
            wget -qO "$tmp/$BIN_NAME" "$url" || fail "download failed"
        else
            fail "need curl or wget to download (or pass --binary)"
        fi
    fi

    chmod 0755 "$tmp/$BIN_NAME"
    mkdir -p "$INSTALL_DIR"
    mv "$tmp/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME.new"
    mv "$INSTALL_DIR/$BIN_NAME.new" "$INSTALL_DIR/$BIN_NAME"
    log "installed $INSTALL_DIR/$BIN_NAME"
}

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
        [ -n "$CORS_ORIGINS" ] && echo "RUNIX_CORS_ORIGINS=$CORS_ORIGINS"
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
    cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=Runix control plane
Documentation=https://github.com/runix/runix
After=network-online.target postgresql.service
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
install_binary
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

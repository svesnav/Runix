#!/bin/sh
# Runix agent installer.
#
#   curl -fsSL https://<control-plane>/install/agent.sh | sh -s -- \
#       --url https://runix.example.com --token rnx_agt_xxx
#
# Re-running upgrades the binary in place and restarts the service; the
# existing configuration is kept unless new values are passed.
#
# POSIX sh on purpose: minimal images often have no bash.
set -eu

BIN_NAME=runix-agent
INSTALL_DIR=${RUNIX_INSTALL_DIR:-/usr/local/bin}
CONFIG_DIR=${RUNIX_CONFIG_DIR:-/etc/runix}
DATA_DIR=${RUNIX_AGENT_DATA_DIR:-/var/lib/runix-agent}
SERVICE_NAME=runix-agent
SERVICE_USER=${RUNIX_AGENT_USER:-root}

SERVER_URL=${RUNIX_AGENT_SERVER_URL:-}
TOKEN=${RUNIX_AGENT_TOKEN:-}
VERSION=${RUNIX_VERSION:-latest}
# Where release archives live. Point at your own mirror if needed.
DOWNLOAD_BASE=${RUNIX_DOWNLOAD_BASE:-https://github.com/runix/runix/releases}
LOCAL_BINARY=""
NO_START=0

usage() {
    cat <<EOF
Runix agent installer

Options:
  --url URL          Control-plane URL (https://host or wss://host)
  --token TOKEN      Agent enrollment token
  --binary PATH      Install this local binary instead of downloading
  --version VERSION  Release to download (default: latest)
  --data-dir PATH    Agent state directory (default: $DATA_DIR)
  --user USER        Run the service as this user (default: root)
  --no-start         Install and configure, but do not start the service
  -h, --help         Show this help

Environment equivalents: RUNIX_AGENT_SERVER_URL, RUNIX_AGENT_TOKEN,
RUNIX_VERSION, RUNIX_DOWNLOAD_BASE, RUNIX_INSTALL_DIR, RUNIX_CONFIG_DIR.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --url) SERVER_URL="$2"; shift 2 ;;
        --token) TOKEN="$2"; shift 2 ;;
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --user) SERVICE_USER="$2"; shift 2 ;;
        --no-start) NO_START=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

log()  { echo "[runix] $*"; }
fail() { echo "[runix] error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root (try: sudo $0 ...)"

# Existing config is reused so an upgrade needs no arguments.
ENV_FILE="$CONFIG_DIR/agent.env"
if [ -z "$SERVER_URL" ] && [ -f "$ENV_FILE" ]; then
    SERVER_URL=$(sed -n 's/^RUNIX_AGENT_SERVER_URL=//p' "$ENV_FILE" | head -n1)
fi
if [ -z "$TOKEN" ] && [ -f "$ENV_FILE" ]; then
    TOKEN=$(sed -n 's/^RUNIX_AGENT_TOKEN=//p' "$ENV_FILE" | head -n1)
fi
[ -n "$SERVER_URL" ] || fail "--url is required (control-plane URL)"
[ -n "$TOKEN" ] || fail "--token is required (agent enrollment token)"

detect_arch() {
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
}

[ "$(uname -s)" = "Linux" ] || fail "the agent installer supports Linux only"
ARCH=$(detect_arch)

install_binary() {
    tmp=$(mktemp -d)
    # shellcheck disable=SC2064 # expand tmp now, not at trap time
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
    # Replace via rename so a running binary is never written in place
    # (that would fail with ETXTBSY).
    mv "$tmp/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME.new"
    mv "$INSTALL_DIR/$BIN_NAME.new" "$INSTALL_DIR/$BIN_NAME"
    log "installed $INSTALL_DIR/$BIN_NAME"
}

write_config() {
    mkdir -p "$CONFIG_DIR"
    umask 077
    cat > "$ENV_FILE" <<EOF
# Written by install-agent.sh. Contains the agent credential: keep 0600.
RUNIX_AGENT_SERVER_URL=$SERVER_URL
RUNIX_AGENT_TOKEN=$TOKEN
RUNIX_AGENT_DATA_DIR=$DATA_DIR
RUNIX_AGENT_LOG_FORMAT=json
EOF
    chmod 0600 "$ENV_FILE"
    mkdir -p "$DATA_DIR"
    if [ "$SERVICE_USER" != "root" ]; then
        id "$SERVICE_USER" >/dev/null 2>&1 || fail "user does not exist: $SERVICE_USER"
        chown -R "$SERVICE_USER" "$DATA_DIR" "$CONFIG_DIR"
    fi
    log "wrote $ENV_FILE"
}

write_service() {
    cat > "/etc/systemd/system/$SERVICE_NAME.service" <<EOF
[Unit]
Description=Runix agent
Documentation=https://github.com/runix/runix
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_DIR/$BIN_NAME
Restart=always
RestartSec=5
# The agent manages host workloads, so it deliberately runs unconfined;
# restrict it here if your deployment does not need every provider.
NoNewPrivileges=no

[Install]
WantedBy=multi-user.target
EOF
    log "wrote /etc/systemd/system/$SERVICE_NAME.service"
}

install_binary
write_config

if [ -d /run/systemd/system ]; then
    write_service
    systemctl daemon-reload
    if [ "$NO_START" -eq 1 ]; then
        systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
        log "installed; start it with: systemctl start $SERVICE_NAME"
    else
        systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
        systemctl restart "$SERVICE_NAME"
        sleep 1
        if systemctl is-active --quiet "$SERVICE_NAME"; then
            log "agent running — check in with: systemctl status $SERVICE_NAME"
        else
            echo "[runix] the service failed to start; recent logs:" >&2
            journalctl -u "$SERVICE_NAME" -n 20 --no-pager >&2 || true
            exit 1
        fi
    fi
else
    log "systemd not detected; run the agent yourself:"
    log "  env \$(grep -v '^#' $ENV_FILE | xargs) $INSTALL_DIR/$BIN_NAME"
fi

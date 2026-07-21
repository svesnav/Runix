#!/bin/sh
# Runix agent installer.
#
#   curl -fsSL https://<control-plane>/install/agent.sh | sh -s -- \
#       --url https://runix.example.com --token rnx_agt_xxx
#
# Everything lands under /opt/runix: the binary, its config, and the
# per-daemon state the agent supervises.
#
# Re-running upgrades the binary in place and restarts the service; the
# existing configuration is kept unless new values are passed.
#
# POSIX sh on purpose: minimal images often have no bash.
set -eu

BIN_NAME=runix-agent
PREFIX=${RUNIX_PREFIX:-/opt/runix}
INSTALL_DIR=${RUNIX_INSTALL_DIR:-}
CONFIG_DIR=${RUNIX_CONFIG_DIR:-}
DATA_DIR=${RUNIX_AGENT_DATA_DIR:-}
SERVICE_NAME=runix-agent
SERVICE_USER=${RUNIX_AGENT_USER:-root}

SERVER_URL=${RUNIX_AGENT_SERVER_URL:-}
TOKEN=${RUNIX_AGENT_TOKEN:-}
VERSION=${RUNIX_VERSION:-latest}
REPO=${RUNIX_REPO:-svesnav/Runix}
# Where release archives live. Point at your own mirror if needed.
DOWNLOAD_BASE=${RUNIX_DOWNLOAD_BASE:-}
# Private repositories serve release assets only through the API, which
# needs a token with `contents:read`.
GITHUB_TOKEN=${RUNIX_GITHUB_TOKEN:-${GITHUB_TOKEN:-}}
LOCAL_BINARY=""
NO_START=0

usage() {
    cat <<EOF
Runix agent installer

Options:
  --url URL           Control-plane URL (https://host or wss://host)
  --token TOKEN       Agent enrollment token
  --binary PATH       Install this local binary instead of downloading
  --version VERSION   Release to download (default: latest)
  --repo OWNER/NAME   GitHub repository to download from (default: $REPO)
  --github-token TOK  Token for downloading from a private repository
  --prefix PATH       Install root (default: $PREFIX)
  --data-dir PATH     Agent state directory (default: \$PREFIX/agent)
  --user USER         Run the service as this user (default: root)
  --no-start          Install and configure, but do not start the service
  -h, --help          Show this help

Environment equivalents: RUNIX_AGENT_SERVER_URL, RUNIX_AGENT_TOKEN,
RUNIX_VERSION, RUNIX_REPO, RUNIX_GITHUB_TOKEN, RUNIX_PREFIX,
RUNIX_INSTALL_DIR, RUNIX_CONFIG_DIR.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --url) SERVER_URL="$2"; shift 2 ;;
        --token) TOKEN="$2"; shift 2 ;;
        --binary) LOCAL_BINARY="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        --repo) REPO="$2"; shift 2 ;;
        --github-token) GITHUB_TOKEN="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --user) SERVICE_USER="$2"; shift 2 ;;
        --no-start) NO_START=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

# Resolved after parsing so --prefix applies to all of them.
[ -n "$INSTALL_DIR" ] || INSTALL_DIR="$PREFIX/bin"
[ -n "$CONFIG_DIR" ] || CONFIG_DIR="$PREFIX/etc"
[ -n "$DOWNLOAD_BASE" ] || DOWNLOAD_BASE="https://github.com/$REPO/releases"

log()  { echo "[runix] $*"; }
fail() { echo "[runix] error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root (try: sudo $0 ...)"
[ "$(uname -s)" = "Linux" ] || fail "the agent installer supports Linux only"

ENV_FILE="$CONFIG_DIR/agent.env"

# Releases before the /opt layout installed to /etc/runix. Carry that file
# forward so an upgrade keeps its enrollment token and data directory.
LEGACY_ENV=/etc/runix/agent.env
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

# Existing config is reused so an upgrade needs no arguments.
[ -n "$SERVER_URL" ] || SERVER_URL=$(read_existing RUNIX_AGENT_SERVER_URL)
[ -n "$TOKEN" ] || TOKEN=$(read_existing RUNIX_AGENT_TOKEN)
# An already-supervised daemon tree must keep its path, so an existing data
# directory always wins over the new default.
[ -n "$DATA_DIR" ] || DATA_DIR=$(read_existing RUNIX_AGENT_DATA_DIR)
[ -n "$DATA_DIR" ] || DATA_DIR="$PREFIX/agent"

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

ARCH=$(detect_arch)

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
Documentation=https://github.com/$REPO
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

mkdir -p "$PREFIX"
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

log "install root: $PREFIX  (state: $DATA_DIR)"

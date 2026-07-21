CREATE TABLE servers (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL UNIQUE,
    description       text NOT NULL DEFAULT '',
    hostname          text NOT NULL DEFAULT '',
    os                text NOT NULL DEFAULT '',
    os_version        text NOT NULL DEFAULT '',
    kernel_version    text NOT NULL DEFAULT '',
    architecture      text NOT NULL DEFAULT '',
    agent_version     text NOT NULL DEFAULT '',
    location          text NOT NULL DEFAULT '',
    tags              text[] NOT NULL DEFAULT '{}',
    labels            jsonb NOT NULL DEFAULT '{}',
    agent_token_hash  bytea UNIQUE,
    cpu_cores         integer NOT NULL DEFAULT 0,
    memory_bytes      bigint NOT NULL DEFAULT 0,
    swap_bytes        bigint NOT NULL DEFAULT 0,
    disk_bytes        bigint NOT NULL DEFAULT 0,
    docker_available  boolean NOT NULL DEFAULT false,
    systemd_available boolean NOT NULL DEFAULT false,
    runtime_types     text[] NOT NULL DEFAULT '{}',
    connection_status text NOT NULL DEFAULT 'never_connected'
                      CHECK (connection_status IN ('never_connected', 'online', 'offline')),
    last_heartbeat_at timestamptz,
    last_seen_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE server_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE server_group_members (
    group_id  uuid NOT NULL REFERENCES server_groups(id) ON DELETE CASCADE,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, server_id)
);

CREATE TABLE server_metrics (
    server_id    uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    cpu_percent  real NOT NULL DEFAULT 0,
    load1        real NOT NULL DEFAULT 0,
    load5        real NOT NULL DEFAULT 0,
    load15       real NOT NULL DEFAULT 0,
    memory_used  bigint NOT NULL DEFAULT 0,
    memory_total bigint NOT NULL DEFAULT 0,
    swap_used    bigint NOT NULL DEFAULT 0,
    swap_total   bigint NOT NULL DEFAULT 0,
    disk_used    bigint NOT NULL DEFAULT 0,
    disk_total   bigint NOT NULL DEFAULT 0,
    net_rx_bytes bigint NOT NULL DEFAULT 0,
    net_tx_bytes bigint NOT NULL DEFAULT 0,
    temperature  real,
    uptime_secs  bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, collected_at)
);

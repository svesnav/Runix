CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username             citext NOT NULL UNIQUE,
    email                citext NOT NULL UNIQUE,
    display_name         text NOT NULL DEFAULT '',
    password_hash        text NOT NULL,
    is_active            boolean NOT NULL DEFAULT true,
    is_system            boolean NOT NULL DEFAULT false,
    must_change_password boolean NOT NULL DEFAULT false,
    totp_enabled         boolean NOT NULL DEFAULT false,
    totp_secret_enc      text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_hash  bytea NOT NULL UNIQUE,
    user_agent    text NOT NULL DEFAULT '',
    ip            text NOT NULL DEFAULT '',
    remember      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    replaced_by   uuid
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE recovery_codes (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    used_at   timestamptz
);
CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id);

CREATE TABLE personal_access_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz,
    UNIQUE (user_id, name)
);

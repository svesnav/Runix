CREATE TABLE roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key         text NOT NULL UNIQUE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    is_system   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id    uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission text NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE user_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_group_members (
    group_id uuid NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

-- Scoped permission grants beyond role membership: to a user or a user
-- group, globally or narrowed to one server group / server / runtime.
CREATE TABLE grants (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type text NOT NULL CHECK (subject_type IN ('user', 'group')),
    subject_id   uuid NOT NULL,
    permission   text NOT NULL,
    scope_type   text NOT NULL CHECK (scope_type IN ('global', 'server_group', 'server', 'runtime')),
    scope_id     text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   uuid,
    UNIQUE (subject_type, subject_id, permission, scope_type, scope_id)
);
CREATE INDEX grants_subject_idx ON grants (subject_type, subject_id);

CREATE TABLE scheduled_tasks (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    server_id     uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('runtime_action', 'runtime_exec')),
    payload       jsonb NOT NULL DEFAULT '{}',
    cron          text NOT NULL,
    enabled       boolean NOT NULL DEFAULT true,
    next_run_at   timestamptz,
    last_run_at   timestamptz,
    last_status   text NOT NULL DEFAULT '' CHECK (last_status IN ('', 'success', 'failure')),
    last_error    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    created_by    uuid,
    UNIQUE (server_id, name)
);
CREATE INDEX scheduled_tasks_due_idx ON scheduled_tasks (next_run_at) WHERE enabled;

CREATE TABLE scheduled_task_runs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     uuid NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    started_at  timestamptz NOT NULL DEFAULT now(),
    duration_ms integer NOT NULL DEFAULT 0,
    status      text NOT NULL CHECK (status IN ('success', 'failure')),
    detail      text NOT NULL DEFAULT ''
);
CREATE INDEX scheduled_task_runs_task_idx ON scheduled_task_runs (task_id, started_at DESC);

CREATE TABLE audit_logs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ts          timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid,
    actor_name  text NOT NULL DEFAULT '',
    ip          text NOT NULL DEFAULT '',
    user_agent  text NOT NULL DEFAULT '',
    request_id  text NOT NULL DEFAULT '',
    action      text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id   text NOT NULL DEFAULT '',
    old_value   jsonb,
    new_value   jsonb,
    result      text NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failure')),
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX audit_logs_ts_idx ON audit_logs (ts DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id);
CREATE INDEX audit_logs_action_idx ON audit_logs (action);
CREATE INDEX audit_logs_target_idx ON audit_logs (target_type, target_id);

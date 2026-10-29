CREATE SCHEMA audit;

-- Same table name as public.users in another schema.
CREATE TABLE audit.users (
    id      bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    action  text   NOT NULL,
    at      timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE audit.users ADD CONSTRAINT audit_users_user_action_key UNIQUE (user_id, action, at);

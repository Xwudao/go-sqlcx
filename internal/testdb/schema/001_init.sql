CREATE TYPE user_status AS ENUM ('active', 'inactive', 'banned');

CREATE TABLE users (
    id          bigserial PRIMARY KEY,
    email       text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    nickname    varchar(50),
    age         integer,
    status      user_status NOT NULL DEFAULT 'active',
    prev_status user_status,
    balance     numeric(12, 2) NOT NULL DEFAULT 0,
    score       double precision,
    is_admin    boolean     NOT NULL DEFAULT false,
    tags        text[]      NOT NULL DEFAULT '{}',
    meta        jsonb,
    settings    json,
    external_id uuid        NOT NULL DEFAULT gen_random_uuid(),
    manager_id  bigint REFERENCES users (id),
    birthday    date,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE users IS 'Application users.';
COMMENT ON COLUMN users.nickname IS 'Optional display name.';

CREATE TABLE orders (
    id         bigserial PRIMARY KEY,
    user_id    bigint         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount     numeric(12, 2) NOT NULL,
    quantity   integer        NOT NULL,
    note       text,
    created_at timestamptz    NOT NULL DEFAULT now()
);

CREATE TABLE categories (
    id        serial PRIMARY KEY,
    parent_id integer REFERENCES categories (id),
    name      text NOT NULL,
    slug      text NOT NULL
);

CREATE UNIQUE INDEX categories_slug_idx ON categories (slug);

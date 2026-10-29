CREATE TYPE post_status AS ENUM ('draft', 'published', 'archived');

CREATE TABLE authors (
    id         bigserial PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    name       text NOT NULL,
    bio        text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE posts (
    id           bigserial PRIMARY KEY,
    author_id    bigint      NOT NULL REFERENCES authors (id),
    title        text        NOT NULL,
    status       post_status NOT NULL DEFAULT 'draft',
    tags         text[]      NOT NULL DEFAULT '{}',
    attrs        jsonb       NOT NULL DEFAULT '{}',
    views        integer     NOT NULL DEFAULT 0,
    published_at timestamptz
);

-- name: CreateAuthor :one
INSERT INTO authors (email, name, bio) VALUES ($1, $2, $3) RETURNING *;

-- name: CreatePost :one
INSERT INTO posts (author_id, title, status, tags) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetAuthor :one
SELECT * FROM authors WHERE id = $1;

-- name: ListPublished :many
-- sqlcx:dynamic
SELECT p.id, p.title, a.name AS author, p.views, p.published_at
FROM posts p
JOIN authors a ON a.id = p.author_id
WHERE p.status = 'published'
ORDER BY published_at DESC;

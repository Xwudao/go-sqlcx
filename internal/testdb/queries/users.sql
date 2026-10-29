-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (email, name, status) VALUES ($1, $2, $3) RETURNING *;

-- name: ListUsersByStatus :many
-- sqlcx:dynamic
SELECT * FROM users
WHERE status = sqlc.arg(status)
ORDER BY created_at DESC, id DESC;

-- name: ListUserOrderTotals :many
-- sqlcx:dynamic
SELECT u.id AS user_id, u.name, count(o.id) AS order_count, coalesce(sum(o.amount), 0)::numeric AS total
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
WHERE u.created_at >= sqlc.arg(since) AND u.status = sqlc.arg(status)
GROUP BY u.id, u.name
ORDER BY total DESC;

-- name: CountUsers :one
SELECT count(*) FROM users;

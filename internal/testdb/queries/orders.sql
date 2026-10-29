-- name: CreateOrder :one
INSERT INTO orders (user_id, amount, quantity, note) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListRecentOrders :many
SELECT * FROM orders ORDER BY created_at DESC LIMIT $1;

-- name: AddAuditEntry :exec
INSERT INTO audit.users (user_id, action) VALUES ($1, $2);

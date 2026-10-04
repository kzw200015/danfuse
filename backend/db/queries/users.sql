-- name: CreateUser :one
INSERT INTO users (name, email)
VALUES ($1, $2)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users
WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT count(*) FROM users;

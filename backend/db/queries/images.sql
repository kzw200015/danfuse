-- name: InsertImage :one
INSERT INTO images (content_type, data, sha256)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetImage :one
-- 图片接口用：原始字节与 content-type。
SELECT content_type, data
FROM images
WHERE id = $1;

-- name: GetImageSHA256 :one
-- 同步时与目录源的新图比较，不取图片本身。
SELECT sha256
FROM images
WHERE id = $1;

-- name: DeleteImage :exec
DELETE FROM images
WHERE id = $1;

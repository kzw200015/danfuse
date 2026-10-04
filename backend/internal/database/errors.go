package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation PostgreSQL 唯一约束冲突错误码，
// 其他错误码见 https://www.postgresql.org/docs/current/errcodes-appendix.html
const pgUniqueViolation = "23505"

// IsUniqueViolation 判断是否违反唯一约束。
func IsUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == pgUniqueViolation
}

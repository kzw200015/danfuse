-- +goose Up
-- 租约：应用的锁（见 database.TryLease、docs/adr/0003）。持有者定时续约，过期后别人才能接管；释放时删除这一行。
-- 进程崩溃时留下的行等过期后被下一个持有者覆盖；之后再也不会被拿的键（例如已删除的季绑定）留下的行不清理。
CREATE SEQUENCE lease_tokens;

CREATE TABLE leases (
    key        TEXT PRIMARY KEY,
    token      BIGINT      NOT NULL, -- fencing token：每次拿到锁从 lease_tokens 取一个新值，续约、释放都核对它
    expires_at TIMESTAMPTZ NOT NULL  -- 按数据库的 now() 判断过期
);

-- +goose Down
DROP TABLE leases;
DROP SEQUENCE lease_tokens;

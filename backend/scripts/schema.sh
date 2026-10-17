#!/usr/bin/env bash
# 把迁移执行到一个空库，按表导出最终结构的 CREATE 语句到 db/schema.sql，用来查看全部表结构、在 PR 里审查表结构的变化。
# 起一个临时的 postgres:18 容器（与 dbtest 同版本），用 goose CLI 执行迁移，再在容器里用 psql 运行 scripts/schema.sql 导出，最后删掉容器。
# 导出后把 db/schema.sql 执行到另一个空库，两边的 pg_dump 必须一致，保证导出没有遗漏迁移里的对象。
# 用法（在 backend/ 下）：scripts/schema.sh <goose 版本>，通常通过 make schema 调用。
set -euo pipefail

goose_version=${1:?usage: scripts/schema.sh <goose-version>}
image=postgres:18
out=db/schema.sql

ctr=$(docker run -d --rm -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=danfuse -p 127.0.0.1::5432 "$image")
trap 'docker rm -f "$ctr" >/dev/null' EXIT

# 镜像初始化时会先起一个只监听 unix socket 的临时实例，等 TCP 可连才算真正就绪
for _ in $(seq 60); do
	docker exec "$ctr" pg_isready -q -h 127.0.0.1 -U postgres && break
	sleep 0.5
done
docker exec "$ctr" pg_isready -q -h 127.0.0.1 -U postgres

port=$(docker port "$ctr" 5432/tcp | head -n1 | sed 's/.*://')
go run "github.com/pressly/goose/v3/cmd/goose@$goose_version" -dir db/migrations \
	postgres "postgres://postgres:postgres@127.0.0.1:$port/danfuse?sslmode=disable" up >/dev/null

{
	echo "-- 由 make schema 根据 db/migrations 生成，不要手改。表结构以迁移为准，这里只用来查看。"
	echo
	docker exec -i "$ctr" psql -X -q -v ON_ERROR_STOP=1 -U postgres -d danfuse <scripts/schema.sql
} >"$out"
echo "wrote $out"

# pg_dump 首尾的 \restrict 带每次随机的密钥，比对前去掉
dump() {
	docker exec "$ctr" pg_dump -U postgres -d "$1" --schema-only --no-owner --no-privileges -T public.goose_db_version |
		grep -Ev '^\\(un)?restrict '
}
docker exec "$ctr" psql -X -q -U postgres -c 'CREATE DATABASE rebuilt'
docker exec -i "$ctr" psql -X -q -v ON_ERROR_STOP=1 -U postgres -d rebuilt <"$out" >/dev/null
if ! diff -u --label migrations --label "$out" <(dump danfuse) <(dump rebuilt); then
	echo "error: $out 与迁移建出的表结构不一致（见上面的 diff），需要在 scripts/schema.sql 里补上漏掉的部分" >&2
	exit 1
fi

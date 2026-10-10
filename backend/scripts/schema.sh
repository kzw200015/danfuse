#!/usr/bin/env bash
# 把迁移执行到一个空库，用 psql 的 \d 导出每张表的最终结构到 db/schema.txt，用来查看全部表结构、在 PR 里审查表结构的变化。
# 起一个临时的 postgres:18 容器（与 dbtest 同版本），用 goose CLI 执行迁移，再在容器里用 psql 导出，最后删掉容器。
# 用法（在 backend/ 下）：scripts/schema.sh <goose 版本>，通常通过 make schema 调用。
set -euo pipefail

goose_version=${1:?usage: scripts/schema.sh <goose-version>}
image=postgres:18
out=db/schema.txt

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

psql() { docker exec "$ctr" psql -X -q -U postgres -d danfuse "$@"; }

# public 下的表和视图（不含 goose 的版本表），按名称排序
relations=$(psql -At -c "SELECT relname FROM pg_class
	WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p', 'v', 'm') AND relname <> 'goose_db_version'
	ORDER BY relname")

{
	echo "由 make schema 根据 db/migrations 生成，不要手改。表结构以迁移为准，这里只用来查看。"
	for rel in $relations; do
		echo
		psql -c "\\d public.$rel"
	done
} | sed -E 's/[[:space:]]+$//' | cat -s >"$out" # psql 的表格行尾有空格，去掉免得编辑器保存时产生 diff
echo "wrote $out"

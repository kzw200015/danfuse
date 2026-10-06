# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

danfuse 是自托管的弹幕聚合服务：从目录源（目前只有 Jellyfin）同步出目录，在集上绑定 B 站弹幕源（或在季上绑定合集，补建出各集的绑定），通过弹弹 API（弹弹play 协议，供 jellyfin-danmaku 插件和支持自定义弹幕 API 的播放器使用）提供弹幕。代码分 `backend/`（Go · Echo v5 · pgx/v5 · sqlc · goose）与 `frontend/`（React 19 · Vite · React Router · TanStack Query · shadcn/ui on Base UI · Tailwind v4）。两端各自构建，命令需在对应目录下执行；前端的构建产物由后端 embed 托管，发布时只有一个二进制。

## 先读哪里

- 术语与业务语义：`GLOSSARY.md`；架构决策：`docs/adr/`。
- 写代码、审查 diff 时对照的规则：`CODING_STANDARDS.md`（事务与加锁、错误与日志、前端反馈等）。
- 改动某个模块前读它的架构文档 `docs/architecture/`：
  - `sources.md`：源适配器、合集与集号规则、弹幕文件、绑定卡片、B 站适配器的测试
  - `season-binding.md`：季绑定、补建、追更
  - `dandan-api.md`：弹弹 API 的接口、响应格式、路由与测试
  - `catalog.md`：目录同步、海报与图片接口、同步状态的轮询、搜索列与 Go 迁移、名称里的季号集号、Jellyfin 样本
  - `runtime.md`：启动流程、后台循环、租约、前端托管
  - `testing.md`：前后端测试的基座与写法

## 常用命令

### backend（`cd backend`）

```sh
cp configs/config.example.yaml configs/config.yaml   # 首次使用；config.yaml 已被 git 忽略
make run                         # 启动（-config configs/config.yaml），启动时自动执行未应用的迁移，默认 :8080
make build                       # 编译到 bin/server
make generate                    # = make sqlc
make migration name=create_xxx   # 用与 go.mod 同版本的 goose CLI 新建 db/migrations 下的迁移
make lint / make fmt             # golangci-lint v2 检查 / 格式化（gofumpt + goimports）
go test ./...                    # Makefile 没有 test 目标；数据库测试需要 Docker，没有 Docker 时直接失败
go test -short ./...             # 跳过数据库测试
go test ./internal/<pkg> -run <TestName>   # 跑单个测试
```

需要可连接的 PostgreSQL；任何配置项可用 `DANFUSE_` 前缀的环境变量覆盖（层级用 `_` 连接，如 `DANFUSE_DATABASE_DSN`）。`-config` 默认为空：不传时只用默认值和环境变量，不读配置文件。

### frontend（`cd frontend`，pnpm，版本由 `packageManager` 固定）

```sh
pnpm dev                         # /api、/dandanplay 代理到 http://localhost:8080，可在 .env.local 用 API_PROXY_TARGET 覆盖
pnpm build                       # tsc -b 类型检查 + vite build，产物直接输出到 backend/web/static/dist
pnpm test:unit --run             # Vitest（不加 --run 为监听模式）
pnpm test:unit --run <path/to/xxx.spec.ts>   # 跑单个文件；用 -t "<用例名>" 过滤用例
pnpm lint / pnpm lint:fix        # oxlint
pnpm format / pnpm format:check  # oxfmt（无分号、单引号）
pnpm dlx shadcn@latest add <component>   # 添加 shadcn/ui 组件到 src/components/ui
```

### 镜像（仓库根目录）

```sh
docker build -t danfuse .        # 多阶段 Dockerfile：Node 构建前端 → Go 编译 → distroless 运行镜像（nonroot，不传 -config）
docker compose up -d             # compose.yaml 是部署示例（danfuse + postgres:18），敏感值放旁边的 .env
```

`compose.yaml` 默认拉 ghcr 上的镜像；验证本地改动时按它的注释改成 `build: .`，或者用 `e2e/compose.yaml`（从本地源码构建，用法见 `e2e/README.md`）。

## 后端架构

- **分层**：`handler` → `service` → `repository.Store` → PostgreSQL，依赖方向由 depguard 守住（`backend/.golangci.yml`）。所有组件在 `internal/app/app.go` 的 `app.New` 里手写组装（不用 DI 框架）。
- **领域包**（包名取自 `GLOSSARY.md`，如 `catalog`、`source`、`danmaku`）只放接口、类型、纯计算与外部适配；外部系统的适配器放在领域包的子包里（`catalog/jellyfin` 实现 `catalog.Source`，`source/bilibili` 实现 `source.Adapter`），由 `app` 装配（目录源按配置的 `kind` 选，未配置时为 nil；源适配器注册进 `source.Registry`）。
- **弹弹 API** 的 handler 在 `dandan` 包，与 `handler` 平级。
- **数据库访问**：service 依赖 `*repository.Store`（具体类型，内嵌 sqlc 生成的 `*repository.Queries`，另有 `ExecTx`；没有 `Querier` 接口）。单条查询直接调用、自动提交；多条语句需要原子性时用 `store.ExecTx(ctx, func(q *repository.Queries) error {...})`。列名 `offset` 是保留字，SQL 里要加引号。
- **生成代码**：`internal/repository/` 下除 `store.go` 外均为 sqlc 生成。sqlc 直接把 `db/migrations`（goose 迁移文件）当作 schema 读取，改表结构 = 新增迁移，再 `make sqlc`。sqlc 配置：JSON tag 为 camelCase、可空列生成指针、`timestamptz` 映射为 `time.Time`、空切片输出 `[]`，个别列在 `sqlc.yaml` 里覆盖为具体的 Go 类型。
- **错误出口**：`server/middleware.go` 的全局 `errorHandler` 统一转换：`*apierr.Error` 按其状态码/业务码输出；Echo 框架错误（404/405 等）沿用状态码、`code=1`；其他未知错误一律 500，不暴露细节。`code` 为 `0` 成功、`1`（`CodeFail`）通用失败，目前没有业务码。
- **日志**：没有请求日志中间件，5xx 由 errorHandler 经 `logger.ServerError` 记录（`request_id`、方法、路由模式、完整的错误链）；请求的 ctx 已取消时改记 info 级别的 `request canceled`。
- **API 版本**：Echo v5 的 handler 签名是 `func(c *echo.Context) error`（指针）；代码使用 Go 1.26+ 的 `errors.AsType`。goimports 本地前缀为 `github.com/kzw200015/danfuse`。

### 新增业务模块的步骤

1. `make migration name=create_xxx`，编写建表 SQL（`-- +goose Up` / `-- +goose Down`）
2. 在 `db/queries/xxx.sql` 写查询，`make sqlc`
3. `internal/service` 写 service（依赖 `*repository.Store`，以及领域包的接口）；要对接外部系统时，接口与交换类型放在领域包，适配器放在它的子包
4. `internal/handler` 写 handler，加入 `Handlers`
5. `internal/server/router.go` 注册路由
6. 在 `internal/app/app.go` 的 `app.New` 里构造 service、handler（适配器也在这里装配）

## 前端架构

- **API 层**：`src/api/request.ts` 的 `request<T>()` 基于 axios（`baseURL: '/api'`，默认 15 秒超时），自动解包统一响应返回 `data`；非 0 业务码、HTTP 错误、网络错误、非统一结构响应都转换为 `ApiError(message, code, status)`（网络错误 `status=0`、`code=CODE_FAIL`）。
- **状态管理**：TanStack Query，全局 `QueryClient`（`src/lib/query-client.ts`）设置 `retry: false`，失败直接展示 `ApiError.message`。查询键与 hook 在 `src/hooks/`（测试自动 mock `@/api/*` 时不会把它们一起替换掉）；让列表的键失效会连同已加载的详情一起刷新。
- **路由**：React Router data mode（从 `react-router` 导入），路由表在 `src/router/routes.ts`（页面用 `lazy` 动态导入 `src/views/*`）；`/` 重定向到 `/catalog`。`App.tsx` 是根布局：顶栏（`Danfuse`、"目录 / 同步"导航、右上角设置弹出层）+ 占满剩余高度的 `<Outlet />` + `Toaster`。
- **UI**：shadcn/ui（style `base-nova`，底层是 Base UI 而非 Radix）；路径别名 `@/*` → `src/*`。

## 文档

`README.md` 面向使用者，只写中文；开发相关的内容（环境、命令、测试、迁移规则、提交约定）放在 `CONTRIBUTING.md`。

## 提交约定

Conventional Commits，scope 用 `backend` / `frontend`（同时改了两端或只改根目录的文件时省略 scope），描述用中文，例如 `feat(backend): 新增 repository.Store，支持在 service 层开启事务`。

## Agent skills

### Issue tracker

Issue 以本地 Markdown 文件形式存放在 `.scratch/<feature>/` 下。详见 `docs/agents/issue-tracker.md`。

### Triage labels

使用默认的五个分诊标签：`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`。详见 `docs/agents/triage-labels.md`。

### Domain docs

single-context：根目录一个 `GLOSSARY.md` 加 `docs/adr/`。详见 `docs/agents/domain.md`。

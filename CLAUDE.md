# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

danfuse 是自托管的弹幕聚合服务：从目录源（目前只有 Jellyfin）同步出目录，在集上绑定 B 站弹幕源（或在季上绑定合集，补建出各集的绑定），通过弹弹 API（弹弹play 协议，供 jellyfin-danmaku 插件和支持自定义弹幕 API 的播放器使用）提供弹幕。代码分 `backend/`（Go · Echo v5 · pgx/v5 · sqlc · goose）与 `frontend/`（React 19 · Vite · React Router · TanStack Query · shadcn/ui on Base UI · Tailwind v4）。两端各自构建，命令需在对应目录下执行；前端的构建产物由后端 embed 托管，发布时只有一个二进制。

## 先读哪里

- 术语与业务语义：`GLOSSARY.md`；架构决策：`docs/adr/`。
- 写代码、审查 diff 时对照的规则：`CODING_STANDARDS.md`（事务与加锁、错误与日志、前端反馈等）。
- 改动某个模块前读它的架构文档 `docs/architecture/`：
  - `sources.md`：源适配器、合集与集号规则、弹幕文件（含文件夹的季绑定的文件）、绑定卡片（含建出的绑定）、B 站适配器的测试
  - `season-binding.md`：表单、派生与槽位，两种季绑定的保存与补建，追更，季面板
  - `scheduled-fetch.md`：定时拉取（绑定建出后的自动重新拉取）
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
make generate                    # 改了 db/migrations 之后：重新生成 sqlc 代码和 db/schema.txt（需要 Docker）
make sqlc                        # 只改了 dao/queries.sql 时重新生成
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

- **按领域分包，领域内分层**：`internal/catalog`（浏览、删除、海报、同步）、`binding`（绑定、弹幕文件、查看弹幕、定时拉取）、`seasonbinding`（合集与文件夹的季绑定、补建、追更）、`dandan`（弹弹 API）、`blockword`（屏蔽词），以及不属于业务的 `health`（健康检查）、`settings`（只读的设置，直接读配置，只有 handler）。每个领域下分三个子包：`handler/`（HTTP：绑定与校验参数、调用 service、输出响应）→ `service/`（业务规则、事务、后台循环）→ `dao/`（`queries.sql` 和 sqlc 生成的代码）→ PostgreSQL，依赖方向由 depguard 守住（`backend/.golangci.yml`）。领域之间只有 service 调用对方的 `Service`（依赖 catalog → seasonbinding → binding，dandan → binding、blockword），不引用对方的 `dao`；要查别的领域的表时在自己的 `queries.sql` 里写查询。各领域的子包都叫 `handler`、`service`、`dao`，在领域外引用时起别名 `<领域>svc`、`<领域>handler`（如 `bindingsvc`）；文档和注释里写 `binding.Service`，指 binding 领域 service 包里的 `Service`。各领域的路由在自己 handler 包的 `routes.go` 里（`Handler.Register`），弹弹 API 的前缀、CORS、Gzip 与 token 也由 `dandan/handler` 自己处理；`server` 只放全局错误出口、前端托管与 HTTP 服务的生命周期，`server/router.go` 的 `server.Handlers` 汇总各领域的 handler、依次调用它们的 `Register`，所有组件在 `internal/app/app.go` 的 `app.New` 里手写组装（不用 DI 框架）。
- **适配器与纯计算包**：外部系统的接口和交换类型放在不访问数据库的包里，适配器放在它的子包里（`catalog/jellyfin` 实现 `catalog.Source`，它在 catalog 的根包，根包只放这个接口和交换类型，`source/bilibili` 实现 `source.Adapter`），由 `app` 装配（目录源按配置的 `kind` 选，未配置时为 nil；源适配器注册进 `source.Registry`）。`source`、`danmaku`、`danmakufile`、`fulltext`、`naming`（季的名称和类别、名称里的季号集号）只放接口、类型和纯计算，不访问数据库。
- **数据库访问**：service 持有 `pool` 和本领域的 `q *dao.Queries`（具体类型，没有 `Querier` 接口）。单条查询直接调用、自动提交；需要原子性时 `pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { q := s.q.WithTx(tx); ... })`；跨领域的事务把 `tx` 传给对方的 `XxxInTx` 方法（如季绑定补建时的 `binding.Service.CreateLinkSlotInTx`）。列名 `offset` 是保留字，SQL 里要加引号。
- **生成代码**：`dao/` 下的 `.go` 由 sqlc 生成，不手改。`sqlc.yaml` 每个领域一组，都直接读 `db/migrations`（goose 迁移文件）作为 schema，只生成本组用到的模型；改表结构 = 新增迁移，再 `make generate`。配置：JSON tag 为 camelCase、可空列生成指针、`timestamptz` 映射为 `time.Time`、空切片输出 `[]`，个别列在 `sqlc.yaml` 里覆盖为具体的 Go 类型。全部表的最终结构看 `db/schema.txt`（`make schema` 由迁移生成，psql `\d` 格式，不手改，CI 检查它与迁移同步）。
- **基础设施**：都在 `internal/infra/` 下，不依赖任何业务包（depguard 守住）：`httpx`（`apierr` API 错误、`response` 统一响应与 5xx 日志、`request` 绑定与校验），`background`（同步与补建共用的后台循环），`config`，`logger`，`database`（连接池、迁移、租约；`dbtest` 是测试基座）。测试用的 `internal/testenv` 按 `app.New` 组装各领域的 service，放共用的辅助函数和假适配器。
- **文件划分**：一个文件一个功能，handler 与 service 里同一功能的文件同名（如 `binding/handler/link.go` 与 `binding/service/link.go`），测试为 `<功能>_test.go`；`service/service.go` 和 `handler/handler.go` 只放骨架：`Service`/`Handler`、构造、包内共用的错误、取/锁与请求结构，以及列出各文件的包文档，不放接口和业务方法（只有一个功能的小包如 blockword、health、settings 除外）。
- **错误出口**：`server/error_handler.go` 的全局 `errorHandler` 统一转换：`*apierr.Error` 按其状态码/业务码输出；Echo 框架错误（404/405 等）沿用状态码、`code=1`；其他未知错误一律 500，不暴露细节。`code` 为 `0` 成功、`1`（`CodeFail`）通用失败，目前没有业务码。
- **日志**：没有请求日志中间件，5xx 由 errorHandler 经 `response.LogServerError` 记录（`request_id`、方法、路由模式、完整的错误链）；请求的 ctx 已取消时改记 info 级别的 `request canceled`。
- **API 版本**：Echo v5 的 handler 签名是 `func(c *echo.Context) error`（指针）；代码使用 Go 1.26+ 的 `errors.AsType`。goimports 本地前缀为 `github.com/kzw200015/danfuse`。

### 新增业务模块的步骤

1. `make migration name=create_xxx`，编写建表 SQL（`-- +goose Up` / `-- +goose Down`）
2. 新建领域目录 `internal/xxx`，在 `internal/xxx/dao/queries.sql` 写查询；在 `sqlc.yaml` 里照样加一组；在 `.golangci.yml` 的 depguard 里把 `xxx/dao` 加进 handler 规则、`xxx/handler` 加进 service 规则，再照样加一条 `xxx-dao`；然后 `make generate`
3. 写 `xxx/service/service.go`（`NewService(pool, ...)`，持有 `dao.New(pool)`）和 `xxx/handler/handler.go`，功能多时按上面的文件划分拆开；要对接外部系统时，接口与交换类型放在领域的根包（如 `catalog.Source`），适配器放在领域的子包
4. 在 `xxx/handler/routes.go` 写 `Register` 注册路由，handler 加进 `server.Handlers`，并在 `server/router.go` 的 `registerRoutes` 里调用它
5. 在 `internal/app/app.go` 的 `app.New` 里构造 service、handler（适配器也在这里装配）；有后台循环的 service 加进 `App.background`；别的领域的测试要用时也加进 `internal/testenv`

## 前端架构

- **API 层**：`src/api/request.ts` 的 `request<T>()` 基于 axios（`baseURL: '/api'`，默认 15 秒超时），自动解包统一响应返回 `data`；非 0 业务码、HTTP 错误、网络错误、非统一结构响应都转换为 `ApiError(message, code, status)`（网络错误 `status=0`、`code=CODE_FAIL`）。
- **状态管理**：TanStack Query，全局 `QueryClient`（`src/lib/query-client.ts`）设置 `retry: false`，失败直接展示 `ApiError.message`。查询键与 hook 在 `src/hooks/`（测试自动 mock `@/api/*` 时不会把它们一起替换掉）；让列表的键失效会连同已加载的详情一起刷新。
- **路由**：React Router data mode（从 `react-router` 导入），路由表在 `src/router/routes.ts`（页面用 `lazy` 动态导入 `src/views/*`）；`/` 重定向到 `/catalog`。`App.tsx` 是根布局：顶栏（`Danfuse`、"目录 / 同步"导航、右上角设置弹出层）+ 占满剩余高度的 `<Outlet />` + `Toaster`。
- **UI**：shadcn/ui（style `base-nova`，底层是 Base UI 而非 Radix）；路径别名 `@/*` → `src/*`。

## 文档

`README.md` 面向使用者，只写中文；开发相关的内容（环境、命令、测试、迁移规则、提交约定）放在 `CONTRIBUTING.md`。

## 提交约定

Conventional Commits，scope 用 `backend` / `frontend`（同时改了两端或只改根目录的文件时省略 scope），描述用中文，例如 `feat(backend): 绑定支持上传弹幕文件`。

## Agent skills

### Issue tracker

Issue 以本地 Markdown 文件形式存放在 `.scratch/<feature>/` 下。详见 `docs/agents/issue-tracker.md`。

### Triage labels

使用默认的五个分诊标签：`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`。详见 `docs/agents/triage-labels.md`。

### Domain docs

single-context：根目录一个 `GLOSSARY.md` 加 `docs/adr/`。详见 `docs/agents/domain.md`。

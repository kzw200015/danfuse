# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

前后端分离的基础框架：`backend/`（Go · Echo v5 · pgx/v5 · sqlc · goose · wire）与 `frontend/`（React 19 · Vite · React Router · TanStack Query · shadcn/ui on Base UI · Tailwind v4）。两端各自独立构建，命令需在对应目录下执行。

## 常用命令

### backend（`cd backend`）

```sh
cp configs/config.example.yaml configs/config.yaml   # 首次使用；config.yaml 已被 git 忽略
make run                         # 启动（-config configs/config.yaml），启动时自动执行未应用的迁移，默认 :8080
make build                       # 编译到 bin/server
make generate                    # = make sqlc + make wire
make migration name=create_xxx   # 用与 go.mod 同版本的 goose CLI 新建 db/migrations 下的迁移
make lint / make fmt             # golangci-lint v2 检查 / 格式化（gofumpt + goimports）
go test ./...                    # Makefile 没有 test 目标
go test ./internal/pkg/errcode -run TestIs   # 跑单个测试
```

需要可连接的 PostgreSQL；任何配置项可用 `DANFUSE_` 前缀的环境变量覆盖（层级用 `_` 连接，如 `DANFUSE_DATABASE_DSN`）。

### frontend（`cd frontend`，pnpm，版本由 `packageManager` 固定）

```sh
pnpm dev                         # /api 代理到 http://localhost:8080，可在 .env.local 用 API_PROXY_TARGET 覆盖
pnpm build                       # tsc -b 类型检查 + vite build
pnpm test:unit --run             # Vitest（不加 --run 为监听模式）
pnpm test:unit --run src/api/__tests__/request.spec.ts   # 跑单个文件；用 -t "<用例名>" 过滤用例
pnpm lint / pnpm lint:fix        # oxlint
pnpm format / pnpm format:check  # oxfmt（无分号、单引号）
pnpm dlx shadcn@latest add <component>   # 添加 shadcn/ui 组件到 src/components/ui
```

## 后端架构

**分层与依赖方向**：`handler` → `service` → `repository.Store` → PostgreSQL。所有组件由 wire 在 `internal/app/wire.go` 组装，`wire_gen.go` 是生成文件。新增/修改构造函数后需加入对应包的 `ProviderSet`（handler 还要加进 `Handlers` 结构体），再执行 `make wire`。

**启动流程**：`app.Init` 在依赖构造阶段完成 配置加载 → 日志 → 连接池 + 自动迁移（`database.NewPool`），`App.Run` 只负责运行 HTTP 服务。迁移文件通过 `db/embed.go` embed 进二进制，用 PG advisory lock 保证多实例只有一个执行迁移。

**生成代码，勿手改**：`internal/repository/` 下除 `store.go` 外均为 sqlc 生成；`internal/app/wire_gen.go` 为 wire 生成。
- sqlc 直接把 `db/migrations`（goose 迁移文件）当作 schema 读取，所以改表结构 = 新增迁移，再 `make sqlc`。
- sqlc 配置：JSON tag 为 camelCase、可空列生成指针、`timestamptz` 映射为 `time.Time`、空切片输出 `[]`。

**数据库访问**：service 依赖 `repository.Store` 接口（`Querier` + `ExecTx`）。单条查询直接调用，自动提交；多条语句需要原子性时用 `store.ExecTx(ctx, func(q repository.Querier) error {...})`，回调内必须用传入的 `q`。唯一约束冲突用 `database.IsUniqueViolation(err)` 判断，查无记录比较 `pgx.ErrNoRows`。

**错误处理与统一响应**（贯穿两端的核心约定）：
- 所有接口返回 `{code, message, data}`，同时保留 REST 语义的 HTTP 状态码；分页 `data = {list, total, page, pageSize}`（`response.NewPage`）。
- handler/service 出错直接 `return errcode.ErrXxx`，按需 `.WithMessage()` 改写提示、`.Wrap(err)` 附带底层原因（只进日志）。`server/middleware.go` 的全局 `errorHandler` 统一转换：`*errcode.Error` 按其状态码/业务码输出；Echo 框架错误（404/405 等）沿用状态码、`code=1`；其他未知错误一律 500，不暴露细节。
- `code`：`0` 成功；`1`（`CodeFail`）通用失败，前端直接提示 message；其他为业务码，**仅在前端需要分支处理时才定义**，按模块分段（用户模块 `10001~10999`），定义在 `internal/pkg/errcode/codes.go`，并必须同步到前端 `src/api/errcode.ts`。
- service 中非业务错误用 `fmt.Errorf("...: %w", err)` 包装返回，会被当作 500。

**handler 参数绑定**：请求结构体用 `param`/`query`/`json` tag，并实现 `Validate() error`（可在其中 trim、填默认值），通过泛型 `bind[xxxRequest](c)` 一次完成绑定 + 校验；校验失败用 `invalidParam("提示语")`。路由统一在 `internal/server/router.go` 的 `/api` 分组下注册。

**配置**：`internal/config` 基于 viper。新增配置项必须在 `setDefaults` 里登记默认值，否则环境变量覆盖不生效（viper `AutomaticEnv` 只认已知 key）；同时更新 `config.example.yaml`。

**API 版本注意**：Echo v5 的 handler 签名是 `func(c *echo.Context) error`（指针）；代码使用 Go 1.26+ 的 `errors.AsType`。golangci-lint 的 goimports 本地前缀为 `github.com/kzw200015/danfuse`（第三方与本项目 import 分组）。

### 新增业务模块的步骤

1. `make migration name=create_xxx`，编写建表 SQL（`-- +goose Up` / `-- +goose Down`）
2. 在 `db/queries/xxx.sql` 写查询，`make sqlc`
3. `internal/service` 写 service（依赖 `repository.Store`），加入 `service.ProviderSet`
4. `internal/handler` 写 handler，加入 `handler.ProviderSet` 与 `Handlers`
5. `internal/server/router.go` 注册路由
6. `make wire`；如有需要前端分支处理的错误，在 `codes.go` 与 `src/api/errcode.ts` 同步新增业务码

## 前端架构

- **API 层**：`src/api/request.ts` 的 `request<T>()` 基于 axios（`baseURL: '/api'`），自动解包统一响应返回 `data`；非 0 业务码、HTTP 错误、网络错误、非统一结构响应都转换为 `ApiError(message, code, status)`（网络错误 `status=0`、`code=CODE_FAIL`）。每个后端模块对应 `src/api/<module>.ts`，类型手写并与后端 camelCase JSON 对齐。
- **状态管理**：服务端数据一律用 TanStack Query（`useQuery` 查询；`useMutation` 成功后 `invalidateQueries` 刷新）。全局 `QueryClient`（`src/lib/query-client.ts`）设置 `retry: false`，失败直接展示 `ApiError.message`。跨组件共享的客户端状态用 Zustand，放在 `src/stores/`（按需创建）；局部状态用 `useState`。
- **路由**：React Router data mode（从 `react-router` 导入，不是 `react-router-dom`），`src/router/index.ts` 中页面用 `lazy` 动态导入 `src/views/*`；`App.tsx` 是根布局（导航 + `<Outlet />` + `Toaster`）。
- **UI**：shadcn/ui（style `base-nova`，底层是 Base UI 而非 Radix），组件通过 CLI 添加到 `src/components/ui/`；路径别名 `@/*` → `src/*`（Vite 通过 `resolve.tsconfigPaths` 读取 tsconfig）。
- **测试**：测试文件放在各目录的 `__tests__/` 下，命名 `*.spec.ts(x)`；jsdom 环境，未开启 globals，需从 `vitest` 显式 import。测试文件被 `tsconfig.app.json` 排除，由 `tsconfig.vitest.json` 单独做类型检查。组件测试用 `vi.mock` 模拟 `@/api/*` 模块，并为每个用例新建 `QueryClient`；`request` 的测试通过替换 `http.defaults.adapter` 模拟响应。

## 提交约定

Conventional Commits，scope 用 `backend` / `frontend`，描述用中文，例如 `feat(backend): 新增 repository.Store，支持在 service 层开启事务`。

## Agent skills

### Issue tracker

Issue 以本地 Markdown 文件形式存放在 `.scratch/<feature>/` 下。详见 `docs/agents/issue-tracker.md`。

### Triage labels

使用默认的五个分诊标签：`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`。详见 `docs/agents/triage-labels.md`。

### Domain docs

single-context：根目录一个 `GLOSSARY.md` 加 `docs/adr/`。详见 `docs/agents/domain.md`。

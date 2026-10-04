# danfuse

前后端分离的基础框架。

| 端 | 技术栈 |
| --- | --- |
| backend | Go · Echo v5 · viper · pgx/v5 + PostgreSQL · sqlc · goose（启动时自动迁移）· wire · golangci-lint v2 |
| frontend | Vue 3 · TypeScript · Vite · Vue Router · Pinia · shadcn-vue（Tailwind CSS v4）· axios · Vitest · oxlint · oxfmt |

## 目录结构

```
backend/
├── cmd/server/            # 程序入口
├── configs/               # config.example.yaml 为配置模板，复制为 config.yaml 使用（已被 git 忽略）
├── db/
│   ├── migrations/        # goose 迁移，embed 进二进制，启动时自动执行
│   └── queries/           # sqlc 查询
├── internal/
│   ├── app/               # wire 依赖注入（wire.go / wire_gen.go）与启动流程
│   ├── config/            # viper 配置加载
│   ├── database/          # pgxpool 连接池、自动迁移、PG 错误判断
│   ├── repository/        # sqlc 生成代码（勿手改）
│   ├── service/           # 业务逻辑
│   ├── handler/           # HTTP 处理
│   ├── server/            # Echo 实例、中间件、全局错误处理、路由
│   └── pkg/
│       ├── errcode/       # 业务错误与错误码
│       ├── response/      # 统一响应结构
│       └── logger/        # slog
├── sqlc.yaml
├── .golangci.yml
└── Makefile

frontend/
├── src/
│   ├── api/               # request.ts 封装统一响应；按模块划分接口
│   ├── components/ui/     # shadcn-vue 组件（通过 CLI 添加）
│   ├── stores/            # Pinia
│   ├── router/
│   └── views/
├── components.json        # shadcn-vue 配置
├── .oxlintrc.json
└── .oxfmtrc.json
```

## 快速开始

### 后端

需要 Go 1.27+、[sqlc](https://docs.sqlc.dev)、[golangci-lint](https://golangci-lint.run) v2，以及一个可连接的 PostgreSQL 数据库。

```sh
cd backend
cp configs/config.example.yaml configs/config.yaml
# 修改 configs/config.yaml 中的 database.dsn，或使用环境变量：
export DANFUSE_DATABASE_DSN="postgres://user:pass@localhost:5432/danfuse?sslmode=disable"
make run        # 启动时自动执行 db/migrations 中未应用的迁移，默认监听 :8080
```

常用命令：

| 命令 | 说明 |
| --- | --- |
| `make run` | 启动服务 |
| `make build` | 编译到 `bin/server` |
| `make migration name=add_xxx` | 用 goose CLI 新建迁移文件 |
| `make sqlc` | 根据 `db/` 生成 `internal/repository` |
| `make wire` | 重新生成 `internal/app/wire_gen.go` |
| `make generate` | sqlc + wire |
| `make lint` / `make fmt` | golangci-lint 检查 / 格式化（gofumpt + goimports） |

### 前端

需要 Node 22.18+ / 24.12+，pnpm 12（已通过 `packageManager` 字段固定）。

```sh
cd frontend
pnpm install
pnpm dev        # /api 代理到 http://localhost:8080
```

后端不在 8080 时，在 `frontend/.env.local` 中设置 `API_PROXY_TARGET=http://localhost:xxxx`。

| 命令 | 说明 |
| --- | --- |
| `pnpm dev` | 开发服务器 |
| `pnpm build` | 类型检查 + 构建 |
| `pnpm test:unit` | Vitest 单元测试（默认监听模式，加 `--run` 只执行一次）；测试文件放在各目录的 `__tests__/` 下 |
| `pnpm lint` / `pnpm lint:fix` | oxlint |
| `pnpm format` / `pnpm format:check` | oxfmt |
| `pnpm dlx shadcn-vue@latest add <component>` | 添加 shadcn-vue 组件 |

## 统一响应

所有接口返回相同结构，HTTP 状态码同时保持 REST 语义：

```json
{ "code": 0, "message": "ok", "data": {} }
```

- `code`：
  - `0` 成功；
  - `1` 通用失败（参数错误、框架错误、服务器内部错误等），前端直接提示 `message` 即可；
  - 其他为业务码，只为前端需要分支处理（跳转、特殊交互等）的场景定义，全局唯一、按模块分段（用户模块 `10001~10999`），
    定义在 `internal/pkg/errcode/codes.go`，前端对应 `src/api/errcode.ts`。
- 分页数据：`data = { list, total, page, pageSize }`。
- handler / service 中直接 `return errcode.ErrXxx`（可用 `.WithMessage()` 改写提示、`.Wrap(err)` 附带底层原因用于日志），由全局错误处理器统一输出；
  路由 404、405 等框架错误沿用框架的 HTTP 状态码，`code` 统一为 `1`；其他未知错误返回 500，不向客户端暴露细节。
- 前端 `src/api/request.ts` 中的 `request<T>()` 会解包 `data`，失败时抛出带 `code`、`status` 的 `ApiError`，
  可按 `err.code === ErrorCode.Xxx` 分支处理。

## 新增一个业务模块

1. `make migration name=create_xxx`，在生成的文件里编写建表 SQL（下次启动时自动执行）
2. 在 `db/queries/xxx.sql` 中编写查询，执行 `make sqlc`
3. 在 `internal/service` 中编写 service，并加入 `service.ProviderSet`
4. 在 `internal/handler` 中编写 handler，加入 `handler.ProviderSet` 与 `Handlers` 结构体
5. 在 `internal/server/router.go` 中注册路由
6. 执行 `make wire`

# danfuse

基于 Go 与 React 的前后端分离项目脚手架，提供分层清晰的后端骨架、类型安全的数据访问、统一的接口响应与错误码，以及配套的现代化前端工程。

## 特性

**后端**

- handler / service / repository 三层结构，使用 wire 在编译期完成依赖注入
- 用 sqlc 从 SQL 生成类型安全的查询代码，通过 pgx 访问 PostgreSQL，并封装事务
- 迁移文件打包进二进制，服务启动时自动执行；多实例同时启动时只有一个实例执行迁移
- 统一响应结构与业务错误码，由全局错误处理器统一输出，未知错误不向客户端暴露细节
- 结构化日志（服务端错误带 Request ID 与完整错误链）、优雅退出
- YAML 配置文件，所有配置项均可被环境变量覆盖

**前端**

- React 19 + TypeScript + Vite
- 类型化的 API 请求层，自动解包统一响应、统一错误类型
- TanStack Query 管理接口数据，Zustand 管理客户端共享状态
- shadcn/ui + Tailwind CSS v4 组件与样式
- Vitest + Testing Library 单元测试，oxlint + oxfmt 代码检查与格式化

## 技术栈

| 端 | 技术栈 |
| --- | --- |
| backend | Go · Echo v5 · viper · pgx/v5 + PostgreSQL · sqlc · goose · wire · golangci-lint v2 |
| frontend | React 19 · TypeScript · Vite · React Router · TanStack Query · Zustand · shadcn/ui（Base UI · Tailwind CSS v4）· axios · Vitest · oxlint · oxfmt |

## 快速开始

### 环境要求

- Go 1.27+
- Node.js 22.18+ 或 24.12+，pnpm 12
- PostgreSQL

### 1. 准备数据库

本地没有 PostgreSQL 时，可以用 Docker 启动一个与默认配置匹配的实例：

```sh
docker run -d --name danfuse-postgres \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=danfuse \
  -p 5432:5432 postgres:18
```

### 2. 启动后端

```sh
cd backend
cp configs/config.example.yaml configs/config.yaml
make run
```

服务默认监听 `:8080`，启动时会自动执行数据库迁移。

### 3. 启动前端

```sh
cd frontend
pnpm install
pnpm dev
```

访问 <http://localhost:5173>。开发服务器会把 `/api` 请求代理到 `http://localhost:8080`。

## 配置

后端配置位于 `backend/configs/config.yaml`，只有传入 `-config` 时才读取（`make run` 会传入），否则只用默认值和环境变量。完整配置项及说明见 [`config.example.yaml`](backend/configs/config.example.yaml)。

所有配置项都可以用 `DANFUSE_` 前缀的环境变量覆盖，层级之间用下划线连接：

```sh
export DANFUSE_DATABASE_DSN="postgres://user:pass@localhost:5432/danfuse?sslmode=disable"
export DANFUSE_SERVER_ADDR=":9090"
```

前端开发时如果后端不在 8080 端口，在 `frontend/.env.local` 中设置 `API_PROXY_TARGET=http://localhost:<port>`。

## 构建与部署

前端的构建产物 embed 进后端二进制，发布时只有一个产物，管理界面与 `/api` 同源，直接刷新前端路由也能打开。

```sh
# Docker 镜像：仓库根目录的多阶段 Dockerfile（Node 构建前端 → Go 编译 → 最小运行镜像）
docker build -t danfuse .

# 自行编译：先构建前端（产物输出到 backend/web/static/dist），再编译后端；迁移文件同样已内置
cd frontend && pnpm build
cd ../backend && make build
./bin/server -config configs/config.yaml
```

没构建前端时后端照常编译运行，访问管理界面只显示"前端未构建"。

根目录的 [`compose.yaml`](compose.yaml) 是部署示例（danfuse + PostgreSQL 18）：按注释修改配置，把 API key 等敏感值写进旁边的 `.env`，再 `docker compose up -d`。容器里不需要配置文件，所有配置都用环境变量。

## 项目结构

```
.
├── backend/
│   ├── cmd/server/     # 程序入口
│   ├── configs/        # 配置模板
│   ├── db/             # 数据库迁移与 SQL 查询
│   ├── internal/       # 应用代码：handler、service、repository、server 等
│   └── web/            # 内嵌的前端构建产物
├── frontend/
│   └── src/
│       ├── api/        # 接口请求
│       ├── components/ # UI 组件
│       ├── router/     # 路由
│       └── views/      # 页面
├── e2e/                # 端到端环境（Jellyfin 10.11、12.1，PostgreSQL 18 与 danfuse）
├── Dockerfile          # 多阶段构建镜像
└── compose.yaml        # 部署示例
```

## 开发

后端代码生成与检查还需要安装 [sqlc](https://docs.sqlc.dev) 和 [golangci-lint](https://golangci-lint.run) v2。

| 后端（`backend/`） | 说明 |
| --- | --- |
| `make run` | 启动服务 |
| `make build` | 编译到 `bin/server` |
| `make generate` | 重新生成 sqlc 与 wire 代码 |
| `make migration name=<name>` | 新建数据库迁移文件 |
| `make lint` / `make fmt` | 代码检查 / 格式化 |
| `go test ./...` | 运行测试（数据库测试需要 Docker，`go test -short ./...` 跳过它们） |

| 前端（`frontend/`） | 说明 |
| --- | --- |
| `pnpm dev` | 启动开发服务器 |
| `pnpm build` | 类型检查并构建 |
| `pnpm test:unit` | 运行单元测试（监听模式，加 `--run` 只执行一次） |
| `pnpm lint` / `pnpm lint:fix` | 代码检查 |
| `pnpm format` / `pnpm format:check` | 代码格式化 |

## 许可证

[MIT](LICENSE)

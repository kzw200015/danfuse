# 参与开发

danfuse 由 `backend/`（Go · Echo v5 · pgx/v5 · sqlc · goose · wire）与 `frontend/`（React 19 · Vite · React Router · TanStack Query · shadcn/ui on Base UI · Tailwind CSS v4）两部分组成，两端各自构建，命令要在对应目录下执行。前端的构建产物内嵌进后端二进制，发布时只有一个产物。

分层、错误处理、测试写法等更细的代码约定见 [`CLAUDE.md`](CLAUDE.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)。

## 环境要求

- Go 1.27+
- Node.js 22.18+ 或 24.12+，pnpm 12（版本由 `frontend/package.json` 的 `packageManager` 固定，`corepack enable` 后自动使用）
- PostgreSQL 18，本地没有时用 Docker 起一个（见下文）
- Docker：后端的数据库测试和端到端环境都要用
- 修改 SQL 查询需要 [sqlc](https://docs.sqlc.dev)，代码检查与格式化需要 [golangci-lint](https://golangci-lint.run) v2；wire 和 goose 不用单独安装，分别通过 `go tool` 和 `go run` 调用

## 项目结构

```
.
├── backend/
│   ├── cmd/server/     # 程序入口
│   ├── configs/        # 配置模板
│   ├── db/             # 数据库迁移与 SQL 查询
│   ├── internal/       # 应用代码：handler、service、repository、server，以及 catalog、source、danmaku 等领域包
│   └── web/            # 内嵌的前端构建产物与占位页
├── frontend/
│   └── src/
│       ├── api/        # 接口请求与类型
│       ├── components/ # 通用组件，components/ui 下是 shadcn/ui 组件
│       ├── hooks/      # 查询键与查询 hook
│       ├── router/     # 路由
│       └── views/      # 页面
├── e2e/                # 端到端环境（Jellyfin 10.11、12.1，PostgreSQL 18 与 danfuse）
├── docs/               # 文档用的图片等
├── Dockerfile          # 多阶段构建镜像
└── compose.yaml        # 部署示例
```

## 本地启动

### 1. 准备数据库

本地没有 PostgreSQL 时，可以用 Docker 启动一个与配置模板匹配的实例：

```sh
docker run -d --name danfuse-postgres \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=danfuse \
  -p 5432:5432 postgres:18
```

也可以直接用[端到端环境](#端到端环境)里的 PostgreSQL 和 Jellyfin。

### 2. 启动后端

```sh
cd backend
cp configs/config.example.yaml configs/config.yaml   # config.yaml 已被 git 忽略
make run
```

服务默认监听 `:8080`，启动时自动执行数据库迁移。`make run` 会传入 `-config configs/config.yaml`；不传 `-config` 时只用默认值和 `DANFUSE_` 前缀的环境变量。要同步目录，在配置里填上 `catalog_source`（可以连端到端环境里的 Jellyfin，见 [`e2e/README.md`](e2e/README.md) 的"连接本地运行的 danfuse"）。

### 3. 启动前端

```sh
cd frontend
pnpm install
pnpm dev
```

打开 <http://localhost:5173>。开发服务器会把 `/api`、`/dandanplay` 请求代理到 `http://localhost:8080`；后端不在这个地址时，在 `frontend/.env.local` 里设置 `API_PROXY_TARGET=http://localhost:<端口>`。

`pnpm build` 把前端构建到 `backend/web/static/dist`，之后编译或 `make run` 的后端就内嵌了它，直接访问 <http://localhost:8080> 即可。没构建时后端照常编译运行，管理界面只显示"前端未构建"。

## 常用命令

| 后端（`backend/`） | 说明 |
| --- | --- |
| `make run` | 启动服务 |
| `make build` | 编译到 `bin/server` |
| `make generate` | 重新生成 sqlc 与 wire 代码（`make sqlc` + `make wire`） |
| `make migration name=<name>` | 新建数据库迁移文件 |
| `make lint` / `make fmt` | 代码检查 / 格式化（gofumpt + goimports） |
| `go test ./...` | 运行全部测试，数据库测试需要 Docker |
| `go test -short ./...` | 跳过数据库测试，不需要 Docker |
| `go test ./internal/<包> -run <测试名>` | 运行单个测试 |

| 前端（`frontend/`） | 说明 |
| --- | --- |
| `pnpm dev` | 启动开发服务器 |
| `pnpm build` | 类型检查并构建，产物输出到 `backend/web/static/dist` |
| `pnpm test:unit --run` | 运行单元测试（不加 `--run` 为监听模式） |
| `pnpm lint` / `pnpm lint:fix` | 代码检查（oxlint） |
| `pnpm format` / `pnpm format:check` | 代码格式化（oxfmt） |

| 镜像（仓库根目录） | 说明 |
| --- | --- |
| `docker build -t danfuse .` | 用多阶段 Dockerfile 构建镜像（Node 构建前端 → Go 编译 → distroless 运行镜像） |

提交前请跑一遍：后端 `make lint` 和 `go test ./...`，前端 `pnpm lint`、`pnpm format:check`、`pnpm build` 和 `pnpm test:unit --run`。

## 测试

### 数据库测试

后端的数据库测试用真实的 PostgreSQL：测试包启动时由 [testcontainers](https://golang.testcontainers.org) 起一个 `postgres:18` 容器，跑一次迁移作为模板库，每个测试从模板复制出自己的库，互不干扰。所以：

- 不加 `-short` 时需要 Docker，没有 Docker 时测试直接失败并提示，不会悄悄跳过。
- `go test -short ./...` 跳过所有需要数据库的测试，只跑纯逻辑测试。

### 外部系统的测试样本

外部系统的适配器平时只回放 `testdata/` 里的样本，不联网；加 `-update` 时才请求真实的服务并重新录制。`-update` 只作用于负责录制的那个用例，其他用例始终只回放。下面的命令都在 `backend/` 下执行。

**Jellyfin**：样本从[端到端环境](#端到端环境)里的 10.11 和 12.1 实例抓取，媒体库是虚构的，不用脱敏。先按 `e2e/README.md` 搭好环境，再：

```sh
go test ./internal/catalog/jellyfin -run TestListSamples -update
```

**B 站**：适配器有一个默认关闭的 live 模式，用一份固定的公开视频列表验证真实的 B 站，CI 不请求 B 站：

```sh
go test ./internal/source/bilibili -run TestLive -args -live           # 只验证
go test ./internal/source/bilibili -run TestLive -args -live -update   # 验证，并重新录制 testdata/
```

- 改动 B 站适配器之后跑一遍。全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的限速，不要反复连续地跑。
- 以未登录的身份请求；港澳台限定番剧的用例要求从中国大陆的网络请求。
- `-update` 先清空 `testdata/`，把响应脱敏后写进去，原始响应不落盘：弹幕正文换成"弹幕1、弹幕2……"，发送者换成固定值，每段只留前若干条；限流、接口异常这类出错的响应不录制。
- 视频列表（集面板的单集、季面板的合集）和脱敏规则见 `internal/source/bilibili/live_test.go` 开头。列表只挑多年不变、内容中性的视频和番剧；某个视频的状态变了（被删除、开关弹幕、改标题），或者列表里连载中的番剧完结了，换一个再重新录制。

### 端到端环境

[`e2e/`](e2e/) 用一个虚构的测试媒体库同时起 Jellyfin 10.11 和 12.1，再加上 PostgreSQL 18 和用本地源码构建的 danfuse，用来验收同步、抓取 Jellyfin 的测试样本、手工试用插件。需要 Docker（含 compose）、ffmpeg、curl、jq：

```sh
cd e2e
./gen-media.sh                                           # 生成测试媒体库到 media/
docker compose up -d jellyfin-10-11 jellyfin-12-1 postgres
./init-jellyfin.sh                                       # 初始化两个 Jellyfin，API key 写进 e2e/.env
docker compose up -d --build danfuse                     # 构建并启动 danfuse，http://localhost:28080
```

切换同步的 Jellyfin 版本、连接本地运行的后端、安装 jellyfin-danmaku 插件、重建环境，以及测试媒体库覆盖的情况，见 [`e2e/README.md`](e2e/README.md)。

## 数据库迁移

- 迁移文件在 `backend/db/migrations`，用 `make migration name=<name>` 新建，写好 `-- +goose Up` 与 `-- +goose Down`。迁移内嵌进二进制，服务启动时自动执行，升级只靠它。
- sqlc 直接把迁移文件当作 schema 读取：改表结构就是新增一个迁移，再 `make sqlc`。
- **迁移文件推到 main 之后就算已经发布，不再修改**（推送 main 会发布镜像，用户的数据库已经执行过它）。改表结构一律新增迁移。
- 已有数据需要用 Go 重新计算时（例如分词规则改变后重算搜索列），在同一目录按序号新增 Go 迁移，写法见 `CLAUDE.md` 的"生成代码"一节。

`backend/internal/repository/`（`store.go` 除外）由 sqlc 生成，`backend/internal/app/wire_gen.go` 由 wire 生成，不要手改；改了查询或构造函数之后执行 `make generate`。

## 文档

- README 面向使用者，只写中文。新增或修改配置项时，同步更新 README 的配置项表、`backend/configs/config.example.yaml`，需要时还有 `compose.yaml`。新增配置项还要在 `backend/internal/config` 的 `setDefaults` 里登记默认值，否则环境变量覆盖不生效。
- README 和其他文档里不写 B 站的接口地址和参数，只说"贴 B 站链接"。

## 提交约定

使用 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)，描述用中文。scope 用 `backend` 或 `frontend`，同时改了两端或只改根目录的文件时省略 scope，例如：

```
feat(backend): 新增 repository.Store，支持在 service 层开启事务
fix(frontend): 偏移输入框失焦时校验
feat: 贴链接支持番剧单集与短链
docs: 补充反向代理的示例
```

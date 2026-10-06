# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

danfuse 是自托管的弹幕聚合服务：从目录源（目前只有 Jellyfin）同步出目录，在集上绑定 B 站弹幕源（或在季上绑定合集，补建出各集的绑定），通过弹弹 API（弹弹play 协议，供 jellyfin-danmaku 插件和支持自定义弹幕 API 的播放器使用）提供弹幕（术语见 `GLOSSARY.md`）。代码分 `backend/`（Go · Echo v5 · pgx/v5 · sqlc · goose）与 `frontend/`（React 19 · Vite · React Router · TanStack Query · shadcn/ui on Base UI · Tailwind v4）。两端各自构建，命令需在对应目录下执行；前端的构建产物由后端 embed 托管，发布时只有一个二进制（见"前端托管"）。

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

需要可连接的 PostgreSQL；任何配置项可用 `DANFUSE_` 前缀的环境变量覆盖（层级用 `_` 连接，如 `DANFUSE_DATABASE_DSN`）。`-config` 默认为空：不传时只用默认值和环境变量，不读配置文件（`make run` 显式传入 `configs/config.yaml`）。

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

`compose.yaml` 默认拉 ghcr 上的镜像（CI 首次推送后才存在）；验证本地改动时按它的注释改成 `build: .`，或者用 `e2e/compose.yaml`：那里的 danfuse 也用这份 Dockerfile 从本地源码构建，用法见 `e2e/README.md`。

## 后端架构

**分层与依赖方向**：`handler` → `service` → `repository.Store` → PostgreSQL。所有组件在 `internal/app/app.go` 的 `app.New` 里手写组装（不用 DI 框架）：新增 service 在那里构造，handler 还要加进 `Handlers` 结构体。`app.New` 只构造对象、不做 IO，构造函数都不连外部系统。
- 凡是读写数据库的业务都在 `service`（例如同步核心在 `SyncService`）。
- 领域包（包名取自 `GLOSSARY.md`，如 `catalog`、`source`）只放接口、类型、纯计算与外部适配，不访问数据库；外部系统的适配器放在领域包的子包里（如 `catalog/jellyfin` 实现 `catalog.Source`，`source/bilibili` 实现 `source.Adapter`）。
- 适配器由 `app` 装配（`internal/app/app.go`：按配置的 `kind` 选目录源，未配置时为 nil；源适配器注册进 `source.Registry`），只有 `app` 引用适配器子包；业务代码只依赖领域包的接口。绑定存的是适配器 ID 加适配器自己的 ref（jsonb），ref 只交给适配器解析：绑定 JSON 里的 `sourceUrl`/`sourceLabel` 一律经适配器的 `Describe` 生成（`service.bindingView`），原始 ref 不对外输出。
- 实现接口的类型（包括测试里的假实现）都在类型定义旁边写编译期断言 `var _ catalog.Source = (*Source)(nil)`：签名不对时在实现处报错，也能用 `= (*` 搜出所有实现。只是嵌入了接口、不实现方法的假类型（如 `platformAdapter`）不写。
- 源适配器只有 `source.Adapter` 一个接口，一个平台一个实现，方法都要实现，业务代码不做类型断言：弹幕源的 `ParseLink`（集面板贴链接得到弹幕源 ref）、`Describe`、`Fetch`，合集的 `ParseCollectionLink`（识别季面板的链接给出候选）、`ListCollection`（按合集 ref 列出条目）、`DescribeCollection`（生成展示的链接和标签）；没有合集的平台 `ParseCollectionLink` 一律返回 `source.ErrUnrecognized`。它只适用于按 ref 能重新拉取的弹幕源，弹幕文件不实现它。合集 ref（`source.CollectionRef`）与弹幕源 ref 是两种类型；合集条目的弹幕源 ref 与单集绑定同一套格式，所以手动绑过的同一个弹幕源能被认出来。集号对应（`source.Mapping`）、集号规则（`source.EpisodeRule`，ADR 0005：投稿合集与多 P 投稿的适配器只给出展开到分 P 的条目和标签、标明 `Collection.NumberedByRule`，序号由季绑定上的规则从标签认出）、条目的整理（`source.NumberItems`：按规则认出序号，再交给 `NormalizeItems` 按 ref 去重、标出重复序号）、默认对应是 `source` 包里的纯计算，不在适配器里做。改集号规则时在同一个事务里按保存的标签重新认序号，补建拉取每个条目之前重新读它的序号（正在拉取的那个照旧按拉取前的序号写入）。B 站适配器的链接解析（`bilibili/link.go`）只做字符串分类（短链先跳转一次再分类），集面板与季面板各自决定接受哪些（集面板遇到合集的链接时提示到季面板）。
- 弹幕文件（ADR 0004）：上传的 B 站 XML 弹幕文件建绑定，不实现 `source.Adapter`。`danmakufile` 领域包只做解析（XML 的解码与字段映射在 `danmaku/bilifmt`，B 站适配器共用）；业务在 `service/binding_file.go`（创建、追加文件、重新解析、文件列表）。绑定的 `kind` 区分 `link` / `file`：文件绑定的 `adapter`、`ref`、`duration` 为空（CHECK 约束守住，唯一约束因此只作用于链接绑定），原文件存 `binding_files`（按 sha256 在绑定内去重），份数 `file_count` 与 `danmaku_count` 一样在事务里维护；弹幕不属于任何平台，原始 ID 直接用 dmid。`bindingView`、`bindingPlatform` 按 kind 分支，只对一种绑定有效的操作用在另一种上时返回 400。上传的 handler 先给请求体套 `http.MaxBytesReader` 再解析 multipart，然后才 `bind`。
- 季绑定（`service.SeasonBindingService`，ADR 0001）：在季上绑定一个合集，补建出普通的绑定（`bindings.season_binding_id` 标明来源）。补建按弹幕源记住处理过的条目（`season_binding_handled`，集删除时级联删除），条目表（`season_binding_items`）是上次检查时的合集内容，条目的状态读取时现算，不存储。`Run` 由 `App.Run` 和同步一起运行：手动触发（创建、立即补建、改集号对应、打开追更）经它的循环拿到租约立即在后台开始，不同季绑定的补建互不等待；追更的扫描由同一个循环每分钟在后台开始一次、一次补建一个，不等上一次扫描做完（同一个季绑定靠它的租约不会重复补建）；到期的判定见 `ListDueSeasonBindings`（从没检查过、满 24 小时、季里有晚于上次检查建出的集、建出的绑定在上次检查之后才满 24 小时；拿到租约之后用同一条查询按 ID 再确认一次，列出之后才被检查过的跳过）。"补建中"以按季绑定的租约为准（`leases` 表里有没有没过期的 `season_backfill:<ID>`），季绑定上不存运行状态。一轮补建在事务之外列出合集、拉取弹幕，写入事务依次以 `FOR KEY SHARE` 锁住季、季绑定、集（先锁季：删季的级联先锁集、后锁季绑定，补建若先锁季绑定、后锁集就会与它死锁）；结束时把上次检查时间写为这一轮的开始时间，被关闭服务或丢失租约打断时不写。追更时间规则写死在 `service/backfill.go`，与追更比较的时间（上次检查、绑定的建出与拉取时间）都取自应用的时钟（Go 的 `time.Now()`），不用数据库的 `now()`，测试才能用假时间推进；集的建出时间仍是数据库写入的。
- 弹弹 API：实现从识别、搜索到取弹幕的接口（`match` 按文件名识别到集；`search/episodes` 一步搜到季和集；`search/anime` 搜到季，再用 `bangumi/{bangumiId}` 取集；`comment` 取弹幕；`related` 是给插件的空兼容接口）。对外的作品（animeId、bangumiId）就是季，episodeId 就是集。`dandan` 包是它的 handler，只做协议参数转换和响应格式化（type、typeDescription、"第N话"、p、cid、平台前缀），与 `handler` 平级；识别、搜索、取季与取弹幕交给 `provider` 包的聚合层 `Aggregator`，它和各 Provider 实现同一个 `provider.Provider` 接口（本地 Provider 要查库，实现在 `service.LocalProvider`），取季、取弹幕按 ID 号段路由，另外用 `Match` 按名称识别一集：名称里要写明集号，其余交给同一个搜索，候选唯一且标题与剧名或原名 `fulltext.SameWords` 时才算确定（`isMatched`）。
- 名称里的季号、集号：搜索关键词和 match 的文件名都用 `catalog.ParseName` 认出写明了的季号、集号（`S01E11`、`S01`、`第2季`、`特别篇`、`第11话`、`EP11`），按季号、集号在 SQL 里精确过滤，集号以 `search/episodes` 的 `episode` 参数优先；按集号过滤时只返回有这一集的季，每季只带这一集（`Season.EpisodeCount` 仍是总集数）。没有标注的数字（"剧名2"、"Mob Psycho 100"）分不出是标题的一部分还是季号，不拆，交给全文搜索：搜索列里季号只以数字出现在最后（`catalog.SearchVector`），不挪动剧名、原名的位置。`SeasonName` 输出的名称（"剧名 第2季""剧名 特别篇"）能被 `ParseName` 拆回剧名和季号。`provider` 的类型是内部结构，不含任何协议格式。本地 Provider 取弹幕时逐个绑定读出落库的弹幕（播放时不向平台现取，不做 live 存储模式，见 `docs/adr/0002`），平台取自绑定的适配器（弹幕文件为无平台），交给 `danmaku.Merge` 做校正和跨源去重；cid 由 `danmaku.CID` 在输出时现算，不存储。
- 目录搜索：`fulltext` 包在 Go 里生成 tsvector、tsquery 的文本（NFKC + 小写，中日韩字符串二元切分），不经过 PostgreSQL 的分词器；`catalog.SearchVector` 拼出一季的搜索列，同步写入一部剧时在同一个事务里重算它所有季的搜索列。

**启动流程**：`cmd/server/main.go` 依次做 配置加载 → 日志 → 连接数据库（`database.Connect`）→ 自动迁移（`database.Migrate`），再用 `app.New` 组装、`App.Run` 运行，不连目录源和 B 站；`App.Run` 用 errgroup 同时运行 HTTP 服务、后台同步（`SyncService.Run`）与季绑定的补建（`SeasonBindingService.Run`；两个 Run 的循环共用 `service/background.go` 的 `backgroundLoop`：定时与手动触发都在循环里处理，后台任务用循环的 ctx，循环返回前等它们结束），HTTP 服务出错（例如端口被占用）时同步与补建也随之退出；Run 等进行中的同步写完最终状态、补建停下才返回，之后 main 才关闭连接池（`defer pool.Close()`）。迁移文件通过 `db/embed.go` embed 进二进制，用 goose 自带的表锁（`goose_lock` 表里的租约）保证多实例只有一个执行迁移；应用自己的锁是 `leases` 表里的租约（ADR 0003，`database.TryLease`，键集中登记在 `internal/database/lease.go`）：持锁期间不占用连接，后台每 10 秒续约、30 秒过期，丢失租约时取消 `Lease.Context()`（持锁做的事都用它），用完调用 `Release`。

**生成代码，勿手改**：`internal/repository/` 下除 `store.go` 外均为 sqlc 生成。
- sqlc 直接把 `db/migrations`（goose 迁移文件）当作 schema 读取，所以改表结构 = 新增迁移，再 `make sqlc`。迁移文件推到 main 之后就算已经发布（推送 main 即发布镜像），不再修改，改表结构一律新增迁移。
- sqlc 配置：JSON tag 为 camelCase、可空列生成指针、`timestamptz` 映射为 `time.Time`、空切片输出 `[]`；个别列在 `sqlc.yaml` 里覆盖为具体的 Go 类型（如 jsonb 的 `sync_runs.warnings` 为 `[]string`，tsvector 的 `seasons.search_vector` 为 `string`）。
- Go 迁移：分词规则（`fulltext`）或搜索列的组成（`catalog.SearchVector`）改变时，已有的搜索列靠 goose 的 Go 迁移重算，不设版本列。目前还没有 Go 迁移，第一次需要时：在 `db/migrations` 下按序号新增 `000NN_xxx.go`（`package migrations`），在 `init` 里用 `goose.AddMigrationContext` 注册一个函数，在迁移的事务里读出所有季和所属的剧，用 `catalog.SearchVector` 算出搜索列写回（goose 从注册它的文件名取版本号）；再在 `database` 包（`migrate.go`）空导入 `db/migrations`，Go 迁移与 SQL 迁移按版本号一起执行。

**数据库访问**：service 依赖 `*repository.Store`（具体类型，不是接口：内嵌 sqlc 生成的 `*repository.Queries`，另有 `ExecTx`；sqlc 不生成 `Querier` 接口）。单条查询直接调用，自动提交；多条语句需要原子性时用 `store.ExecTx(ctx, func(q *repository.Queries) error {...})`，回调内必须用传入的 `q`。唯一约束冲突用 `database.IsUniqueViolation(err)` 判断，查无记录比较 `pgx.ErrNoRows`。
- 并发：保持默认的 READ COMMITTED，不在应用里加内存锁。网络请求（取目录源、拉取弹幕）都在事务之外，拿到结果才开写入事务；写入事务的第一句锁住要写的行（例如创建绑定时 `LockEpisode` 以 `FOR KEY SHARE` 锁住集，查不到就返回"这一集已被删除"的 404；重新拉取、标为失效时 `LockBinding` 以 `FOR UPDATE` 锁住绑定，同一个绑定的写入排队执行，查不到就返回"绑定已被删除"的 404），不靠捕获外键错误；一个事务要锁一个层级以下的多行时，第一句先锁住它们共同的上层行（目前是季），删除的级联才不会和它按相反的顺序加锁（级联的下一层排到外层语句之后执行，删季的顺序是季 → 集 → 季绑定 → 绑定、处理过的记录、条目）；拉取前的查重只为省一次请求，并发重复以唯一约束为准。写回时只更新自己负责的列（拉取不覆盖 offset）。删除剧用 `DeleteSeries` 的 `RETURNING poster_image_id` 拿到海报 ID，在同一个事务里再删这张图，不先 SELECT；`series.poster_image_id` 不级联，顺序固定：同步换图为插入新图 → 剧指向新图 → 删除旧图，删除剧为先删剧、再删图。
- 批量写入用数组参数加 `unnest` 一条语句写完（如 `InsertDanmaku`，`ON CONFLICT DO NOTHING` 按主键去重，`:execrows` 返回实际插入的条数）；`bindings.danmaku_count` 这类计数在同一个事务里按插入的条数维护，读取时不 COUNT。列名 `offset` 是保留字，SQL 里要加引号。

**错误处理与统一响应**（贯穿两端的核心约定）：
- 所有接口返回 `{code, message, data}`，同时保留 REST 语义的 HTTP 状态码。
  - 例外：图片接口 `GET /api/images/:id` 成功时直接返回原始字节和存下的 content-type（`c.Blob`），加 `Cache-Control: public, max-age=31536000, immutable`（海报换了会换新的图片 ID）；出错时照常 return error，由 errorHandler 输出统一结构。前端用 `src/api/images.ts` 的 `imageUrl(id)` 交给 `<img>` 加载，不经过 `request()`。
  - 例外：弹弹 API（`/dandanplay[/<token>]/api/v2` 下）按官方 Swagger 的结构输出（`comment` 只有 `{count, comments}`，其余为 ResponseBase 加数据字段；Swagger 里的字段一个不少，目录里没有的信息输出零值：不可空的为 false、0，列表任何时候都是 `[]`，其余为 null；业务上的空返回 200 加空列表；`bangumi` 找不到作品时返回 200、`success:false`、`errorCode:404`、`bangumi:null`），不用 `response` 包。服务端故障时 `dandan` 的 handler 自己返回 500 和弹弹play 结构的响应体（`comment` 为 `{"count":0,"comments":[]}`，不加 `success`、`errorCode`），不交给 errorHandler，并调用 `logger.ServerError` 记日志；前缀下路由不匹配的 404、405 仍走全局处理。
- handler/service 出错直接 `return apierr.ErrXxx`（通用错误有 `ErrBadRequest`、`ErrNotFound`、`ErrConflict`、`ErrUnprocessable`、`ErrBadGateway`、`ErrInternal`、`ErrServiceUnavailable`），按需 `.WithMessage()` 改写提示、`.Wrap(err)` 附带底层原因（只进日志）。`server/middleware.go` 的全局 `errorHandler` 统一转换：`*apierr.Error` 按其状态码/业务码输出；Echo 框架错误（404/405 等）沿用状态码、`code=1`；其他未知错误一律 500，不暴露细节。
- `apierr` 只用于管理 API 的出口（handler，以及 service 里会返回给接口的错误），领域包不引用它。外部系统的错误由领域包自己定义类型，Message 是适配器写的给用户看的提示，Err 是只进日志的底层原因，调用方按自己的场景处理：源适配器返回 `*source.Error`（带 Kind，追更、重试、标为失效都按 Kind 分支，管理 API 由 `service.sourceAPIError` 按 Kind 转成 400/422/502，只包装 Err，日志里提示不重复）；目录源返回 `*catalog.Error`（同步页上的失败原因和海报警告只显示它的 Message）。后台任务存给管理界面看的原因遇到其他错误（写库失败等）时一律为 `internalErrorMessage`（"服务器内部错误，详见日志"），完整的错误进日志。
- 日志：没有请求日志中间件，5xx 由 errorHandler 记录：按 error 级别记 `request_id`（取自 `X-Request-Id` 响应头）、方法、路由（注册时的路径模式，不含实际 URL）和完整的错误链，字段由 `logger.ServerError` 统一输出；请求的 ctx 已取消（客户端断开）时改记 info 级别的 `request canceled`；4xx 不记录。自己写 5xx 响应、不经过 errorHandler 的 handler 也调用它。
- `code`：`0` 成功；`1`（`CodeFail`）通用失败，前端直接提示 message；其他为业务码，**仅在前端需要分支处理时才定义**，按模块分段（每个模块 1000 个号段），定义在 `internal/pkg/apierr/codes.go`，并必须同步到前端 `src/api/errcode.ts`。目前没有业务码。
- service 中非业务错误用 `fmt.Errorf("...: %w", err)` 包装返回，会被当作 500。

**前端托管**：前端由后端 embed 托管，管理界面与 `/api` 同源。`pnpm build` 直接输出到 `backend/web/static/dist`（不进 git），`backend/web/embed.go` 把它内嵌进二进制；没构建时内嵌的是提交进仓库的占位页 `static/placeholder/`，`go build`、`make run` 照常可用，管理界面只显示"前端未构建"。`server/frontend.go` 用 Echo 的 `middleware.StaticWithConfig`（`HTML5: true`）：文件存在就返回，路由和文件都没命中时回退到 `index.html`，直接刷新前端路由也能打开；`Skipper` 跳过 `/api/`、`/dandanplay/`，这两个前缀下没命中的路由仍按原来的方式返回 404（新增后端路由前缀时加进 `backendPrefixes`）。缓存头：`/assets/*` 长期缓存加 immutable，不存在的直接 404、不回退；其余 `no-cache`。开发时照旧用 `pnpm dev`，Vite 把 `/api`、`/dandanplay` 代理到后端。

**handler 参数绑定**：请求结构体用 `param`/`query`/`json` tag，并实现 `Validate() error`（可在其中 trim、填默认值），通过泛型 `bind[xxxRequest](c)` 一次完成绑定 + 校验；校验失败用 `invalidParam("提示语")`。路由统一在 `internal/server/router.go` 的 `/api` 分组下注册。弹弹 API 在同一文件的 `registerDandanRoutes` 里注册：配置了 `dandanplay.token` 时前缀注册为 `/dandanplay/:token/api/v2`，由组中间件常数时间比较 token，不对时 404（5xx 日志里记的路由模式因此不含 token）；CORS（带 Origin 的请求回 `Access-Control-Allow-Origin: *`，允许 GET、POST（`match`），预检回显请求头）与 Gzip 只挂在这个路由组上，管理 API 不加。

**测试**：数据库测试用真实的 PostgreSQL，基座是 `internal/database/dbtest`：测试包的 `TestMain` 里调用 `dbtest.Main(m)`（testcontainers 起一个 `postgres:18` 容器，跑一次迁移作为模板库），测试里 `pool := dbtest.Pool(t)` 拿到从模板复制出的独立库（可配合 `repository.NewStore(pool)`），测试之间互不干扰，可以 `t.Parallel()`。`-short` 时 `dbtest.Pool` 跳过当前测试。要自己创建连接池时用 `dbtest.Config(t)`，它建好库、登记删库，只返回连接配置。HTTP 测试在 `server` 包内用 `New(...)` 组装完整的 Echo，经 `httptest` 发请求；要换掉托管的前端文件时用 `newServer(..., fstest.MapFS{...})`。
- 弹弹 API 的测试在 `internal/server/dandan_test.go`。插件不调用的 `search/anime`、`bangumi`、`match` 按官方 Swagger 的结构断言完整 JSON，`TestSearchAnimeBangumiComment` 检查三步的 ID 前后衔接、两条搜索路径列出的集相同（`dandanPost` 发 `match` 的 POST；名称的解析规则在 `catalog` 包的 `TestParseName` 单独测）。插件契约测试按 jellyfin-danmaku 插件实际的调用方式重放请求（`pluginGet` 跨域、带 `Accept-Encoding`，关键词不做 URL 编码、按浏览器的规则转义），按插件的读法断言（`pluginMatch` 取 `animes[0]`，按"集号 − 首集标题里第N话的 N"取集；`pluginComments` 把 `p` 按逗号拆成时间、模式、颜色、用户，按用户名前缀分来源）；目录用 `syncCatalog` 经一次真实的同步写入，搜索列由同步核心算出，绑定与弹幕在同步之外用 SQL 补写（`newCommentServer`）。
- service 测试只用真实数据库加假适配器（实现领域包的接口，如 `catalog.Source`、`source.Adapter`），不替换 `repository.Store`。并发用例让假适配器停在 channel 上（例如 `binding_test.go` 的 `fakeAdapter` 在 `started` 上报到、等 `release` 放行；`season_binding_test.go` 的 `fakeCollector` 每次拉取在 `started` 上报弹幕源名字、从 `release` 收到一次放行），期间直接执行 SQL（删除集等），再放行；`assertInvariants` 检查不变量：每个绑定的 `danmaku_count` 等于它实际的弹幕条数、`file_count` 等于它的弹幕文件份数，images 表里没有孤儿图片，处理过的记录都指向存在的集，带 `season_binding_id` 的绑定所在的集属于那个季绑定的季（绑定测试由 `newBindingService` 在每个用例结束时检查，气泡里的测试由 `syncTest` 检查）。季绑定的测试都在 synctest 气泡里（`newSeasonEnv`：第 1 季的集按假时间写入建出时间，`SeasonBindingService.Run` 在后台运行），`synctest.Wait` 等后台的补建停下，`time.Sleep` 推进追更的扫描和 24 小时、14 天的边界。涉及后台 goroutine、定时器的测试（`SyncService.Run`）放在 `testing/synctest` 的气泡里，用 `synctest.Wait` 等后台停下、用假时间推进定时器，不靠 sleep；这时连接池要在气泡里用 `dbtest.Config(t)` 新建、在气泡里关闭（pgx 连接内部的 channel 不能跨气泡使用），写法见 `internal/service/helpers_test.go`（`syncTest` 在每个用例结束时检查不变量：images 表里没有不被任何剧引用的图片）。注意气泡里的定时器和 `context.WithTimeout` 都用假时间，阻塞在数据库 I/O 上时假时间不前进、超时不会触发：数据库卡住时测试会一直挂到 `go test -timeout`。
- 外部系统适配器的测试用 `httptest` 回放 `testdata/` 里的真实样本，不联网；加 `-update` 时从 `e2e/` 环境重新抓取（例如 `go test ./internal/catalog/jellyfin -run TestListSamples -update`，需要先按 `e2e/README.md` 搭好环境；`-update` 只作用于负责录制的那个用例，其他用例始终只回放）。Jellyfin 的 JSON 样本按"路径 + ParentId"命名，海报样本按条目 Id 存成原始图片 `image-<Id>.jpg`/`.png`，回放时 Content-Type 按内容识别。
- B 站适配器（`internal/source/bilibili`）平时同样只回放 `testdata/` 里脱敏后的样本。live 模式请求真实的 B 站，默认关闭，CI 不请求：`go test ./internal/source/bilibili -run TestLive -args -live` 只验证；再加 `-update` 时先清空 `testdata/`，把响应脱敏后写进去（原始响应不落盘，限流、接口异常这类出错的响应不录制）。在改动 B 站适配器之后和里程碑验收时各跑一遍，全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速；以未登录的身份请求，港澳台限定番剧的用例要求从大陆的网络请求。固定的公开视频列表（集面板的单集、季面板的合集）、脱敏规则见 `live_test.go` 开头，列表只挑内容中性的视频和番剧，状态变了（包括列表里连载中的番剧完结了）就换一个再重新录制。测试里假 B 站换掉的是适配器 `http.Client` 的 Transport：各个域名（API、XML 弹幕、短链）的请求都转给它，按 Host 区分接口。

**配置**：`internal/config` 基于 viper。新增配置项必须在 `setDefaults` 里登记默认值，否则环境变量覆盖不生效（viper `AutomaticEnv` 只认已知 key）；同时更新 `config.example.yaml` 和 README 的配置项表，需要时还有 `compose.yaml`。

**API 版本注意**：Echo v5 的 handler 签名是 `func(c *echo.Context) error`（指针）；代码使用 Go 1.26+ 的 `errors.AsType`。golangci-lint 的 goimports 本地前缀为 `github.com/kzw200015/danfuse`（第三方与本项目 import 分组）。

### 新增业务模块的步骤

1. `make migration name=create_xxx`，编写建表 SQL（`-- +goose Up` / `-- +goose Down`）
2. 在 `db/queries/xxx.sql` 写查询，`make sqlc`
3. `internal/service` 写 service（依赖 `*repository.Store`，以及领域包的接口）；要对接外部系统时，接口与交换类型放在领域包，适配器放在它的子包
4. `internal/handler` 写 handler，加入 `Handlers`
5. `internal/server/router.go` 注册路由
6. 在 `internal/app/app.go` 的 `app.New` 里构造 service、handler（适配器也在这里装配）；如有需要前端分支处理的错误，在 `codes.go` 与 `src/api/errcode.ts` 同步新增业务码

## 前端架构

- **API 层**：`src/api/request.ts` 的 `request<T>()` 基于 axios（`baseURL: '/api'`，默认 15 秒超时），自动解包统一响应返回 `data`；非 0 业务码、HTTP 错误、网络错误、非统一结构响应都转换为 `ApiError(message, code, status)`（网络错误 `status=0`、`code=CODE_FAIL`）。每个后端模块对应 `src/api/<module>.ts`，类型手写并与后端 camelCase JSON 对齐。服务端要等较久的请求在 API 模块里单独放宽 `timeout`，统一用 `request.ts` 的 `slowRequestTimeout`（如 `bindings.ts` 的创建绑定、重新拉取：后端当场拉取，最长约 25 秒），界面上用 `useElapsed`（`src/hooks/use-elapsed.ts`）显示已用秒数。
- **状态管理**：服务端数据一律用 TanStack Query（`useQuery` 查询；`useMutation` 成功后 `invalidateQueries` 刷新）。全局 `QueryClient`（`src/lib/query-client.ts`）设置 `retry: false`，失败直接展示 `ApiError.message`。跨组件共享的客户端状态用 Zustand，放在 `src/stores/`（按需创建）；局部状态用 `useState`。
  - 查询键与查询 hook 放在 `src/hooks/use-<资源>.ts`，API 模块只放请求函数和类型（测试自动 mock `@/api/*` 时不会把 hook 和查询键一起替换掉）。查询键：列表 `['<资源>']`、详情 `['<资源>', id]`，例如 `use-series.ts` 的 `seriesKeys`；让列表的键失效会连同已加载的详情一起刷新。
  - 绑定卡片（`BindingCard`）按 `kind` 分出两组操作（`BindingSourceActions.tsx`：重新拉取 / 追加文件、重新解析），各自持有 mutation；一个绑定上改动弹幕的变更都带 `bindingKeys.write(id)` 前缀（`use-bindings.ts`），卡片用 `useIsMutating` 在任何一个进行中时禁用其他操作。弹幕文件列表的键 `['binding-files', id]`，弹出层打开时才取。
  - 季绑定的详情（`use-season-bindings.ts`，键 `['season-bindings', id]`）只在展开条目表、或正在补建时取，正在补建时每秒轮询，结束后让剧详情失效；季绑定的列表在剧详情里。创建、立即补建、改集号对应、开关追更成功后用 `useWatchSeasonBinding` 把最新的详情放进缓存，轮询随即开始。
  - 同步状态：根布局调用一次 `useLatestSyncRun`（`src/hooks/use-sync-runs.ts`），一直每 2 秒轮询最近一次同步（`GET /api/sync-runs/latest`），看到更新的一次同步结束后让剧列表和剧详情（`seriesKeys.list` 前缀）失效；同步列表只在同步页每 2 秒轮询，选中的那次还在运行时它的详情也每 2 秒轮询。手动触发同步在同步开始后返回 202 和它的 ID（toast 提示"已开始同步 #N"）；已有同步在跑时为 409"同步正在进行"，显示在按钮下方。
  - 操作反馈：成功用 toast；失败用 `components/ErrorNote` 显示在出错的位置，保留到下次操作或手动关闭（例外：立即补建被拒绝的 409 用 toast）。删除这类不可恢复的操作先用 `components/ConfirmButton` 确认，确认框写明后果；确认后确认框随即关闭，进行中的状态和失败提示显示在页面上（例如绑定卡片），不留在确认框里。
- **路由**：React Router data mode（从 `react-router` 导入，不是 `react-router-dom`），路由表在 `src/router/routes.ts`（页面用 `lazy` 动态导入 `src/views/*`），`src/router/index.ts` 据此创建 browser router；`/` 重定向到 `/catalog`。`App.tsx` 是根布局：顶栏（`Danfuse`、"目录 / 同步"导航、右上角设置弹出层）+ 占满剩余高度的 `<Outlet />` + `Toaster`。
- **UI**：shadcn/ui（style `base-nova`，底层是 Base UI 而非 Radix），组件通过 CLI 添加到 `src/components/ui/`；路径别名 `@/*` → `src/*`（Vite 通过 `resolve.tsconfigPaths` 读取 tsconfig）。
- **测试**：测试文件放在各目录的 `__tests__/` 下，命名 `*.spec.ts(x)`；jsdom 环境，未开启 globals，需从 `vitest` 显式 import。测试文件被 `tsconfig.app.json` 排除，由 `tsconfig.vitest.json` 单独做类型检查。组件测试用 `vi.mock('@/api/<资源>')` 自动 mock 请求函数，并为每个用例新建 `QueryClient`；`request` 的测试通过替换 `http.defaults.adapter` 模拟响应；涉及路由的测试用 `src/__tests__/utils.tsx` 的 `renderRoutes(path)`（`createMemoryRouter(routes)` 加新的 `QueryClient`，根布局会取最近一次同步和设置，所以要 mock `@/api/sync`、`@/api/settings`，再调用同一文件的 `mockRootLayout()`）；同一文件里还有共用的 fixture 与 mock（`settings`、`binding`、`lighthouse`、`mockCatalog`、`syncRun`、`mockSyncRuns`、`seedSeries`、`card`），新用例先找这里。涉及轮询的用例用 `vi.useFakeTimers({ shouldAdvanceTime: true })`，`vi.advanceTimersByTimeAsync` 推进轮询；点按钮前先等依赖的查询取到（按钮渲染出来时查询可能还没发出）。jsdom 缺少的 `matchMedia`、`scrollIntoView` 在 `vitest.setup.ts` 里补上。

## 文档

`README.md` 面向使用者，只写中文：部署、配置项表、插件设置、反向代理、各项操作的含义和已知限制；改动用户能看到的行为、配置项或部署方式时同步更新。开发相关的内容（环境、命令、测试、迁移规则、提交约定）放在 `CONTRIBUTING.md`。README 和其他文档里不写 B 站的接口地址和参数，只说"贴 B 站链接"。

## 提交约定

Conventional Commits，scope 用 `backend` / `frontend`（同时改了两端或只改根目录的文件时省略 scope），描述用中文，例如 `feat(backend): 新增 repository.Store，支持在 service 层开启事务`。

## Agent skills

### Issue tracker

Issue 以本地 Markdown 文件形式存放在 `.scratch/<feature>/` 下。详见 `docs/agents/issue-tracker.md`。

### Triage labels

使用默认的五个分诊标签：`needs-triage`、`needs-info`、`ready-for-agent`、`ready-for-human`、`wontfix`。详见 `docs/agents/triage-labels.md`。

### Domain docs

single-context：根目录一个 `GLOSSARY.md` 加 `docs/adr/`。详见 `docs/agents/domain.md`。

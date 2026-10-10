# 代码规范

审查 diff 时逐条对照的规则。lint 已经管住的（包依赖方向见 `backend/.golangci.yml` 的 depguard，前端的 import 限制见 `frontend/.oxlintrc.jsonc`）不在这里重复。各模块自己的约定见 `docs/architecture/`。

## Go 结构

- 实现接口的类型（包括测试里的假实现）在类型定义旁边写编译期断言 `var _ catalog.Source = (*Source)(nil)`。只嵌入接口、不实现方法的假类型（如 `platformAdapter`）不写。
- 业务代码只依赖领域包的接口，不对适配器做类型断言；绑定、合集的 ref 只交给适配器解析，原始 ref 不出现在 JSON 输出里。
- 业务按领域分包（`catalog`、`binding`、`seasonbinding`、`dandan`、`blockword`）：`handler*.go` 只绑定与校验参数、调用本领域的 `Service`、输出响应；业务规则和读写数据库都在 service，读写数据库经本领域的 `xxxdb`。
- 领域之间只调用对方 `Service` 的方法（例外是 handler 可以复用对方导出的读请求的函数，如按季上传的 `binding.ReadSeasonUpload`），不引用对方的 `xxxdb`，依赖不成环（catalog → seasonbinding → binding，dandan → binding、blockword）；要查别的领域的表时在自己的 `queries.sql` 里写查询。需要同一个事务时，调用方开事务，把 `pgx.Tx` 传给对方的 `XxxInTx` 方法（如 `binding.Service.CreateBackfilledInTx`），提交之后的收尾（如 `LogFetched`）由调用方做。
- 适配器（领域的子包，如 `catalog/jellyfin`、`source/bilibili`）和纯计算包（`source`、`danmaku`、`danmakufile`、`fulltext`、`catalog/naming`）只放接口、类型、纯计算与外部适配，不访问数据库。
- `app.New` 和各构造函数只构造对象，不做 IO、不连外部系统。新增 service 在 `app.New` 构造，handler 还要加进 `server.Handlers`。
- `xxxdb/` 下 sqlc 生成的 `.go` 不手改；改表结构是新增迁移再 `make generate`（sqlc 代码和 `db/schema.txt` 一起更新）。已推到 main 的迁移文件不再修改。

## 数据库与并发

- 隔离级别保持默认的 READ COMMITTED，不在应用里加内存锁。
- 网络请求（取目录源、拉取弹幕）在事务之外，拿到结果才开写入事务；事务用 `pgx.BeginFunc(ctx, s.pool, ...)`，回调里只用 `s.q.WithTx(tx)` 得到的 `q`（以及把 `tx` 传给别的领域）。
- 写入事务的第一句锁住要写的行（`LockEpisode` 以 `FOR KEY SHARE` 锁集，查不到返回"这一集已被删除"的 404；`LockBinding` 以 `FOR UPDATE` 锁绑定，查不到返回"绑定已被删除"的 404），不靠捕获外键错误。
- 一个事务要锁一个层级以下的多行时，第一句先锁它们共同的上层行（目前是季），和删除的级联保持同一个加锁顺序（删季的顺序是季 → 集 → 季绑定 → 绑定、处理过的记录、条目）。
- 拉取前的查重只为省一次请求，并发重复以唯一约束为准（`database.IsUniqueViolation`）；查无记录比较 `pgx.ErrNoRows`。
- 写回时只更新自己负责的列（例如拉取不覆盖 offset）。
- 批量写入用数组参数加 `unnest` 一条语句写完，`ON CONFLICT DO NOTHING` 去重、`:execrows` 返回实际插入的条数；`danmaku_count`、`file_count` 这类计数在同一个事务里按插入的条数维护，读取时不 COUNT。
- 与追更、定时拉取比较的时间取自应用的时钟（`time.Now()`），不用数据库的 `now()`。

## 错误与日志

- 管理 API 统一返回 `{code, message, data}`，同时保留 REST 语义的 HTTP 状态码。例外只有图片接口和弹弹 API（见 `docs/architecture/catalog.md`、`dandan-api.md`）。
- handler/service 出错直接 `return apierr.ErrXxx`，按需 `.WithMessage()` 改写提示、`.Wrap(err)` 附带底层原因；非业务错误用 `fmt.Errorf("...: %w", err)` 包装，由 errorHandler 当作 500。
- 业务码只在前端需要分支处理时才定义，按模块分段（每个模块 1000 个号段），`internal/httpx/apierr/codes.go` 与前端 `src/api/errcode.ts` 同步新增。
- 外部系统的错误由领域包自己定义类型（`*source.Error`、`*catalog.Error`），Message 给用户看，Err 只进日志。后台任务存给管理界面看的原因遇到其他错误时一律为 `internalErrorMessage`，完整的错误进日志。
- 自己写 5xx 响应、不经过 errorHandler 的 handler 调用 `response.LogServerError` 记日志；4xx 不记录。

## handler 与配置

- 请求结构体用 `param`/`query`/`json` tag，实现 `Validate() error`（可在其中 trim、填默认值，把参数解析、整理成 service 要的形状放进未导出字段，例如解析集号规则、把按季上传的路径分成条目；只看请求本身就能判断的格式校验都放这里，要查库或属于业务规则的放 service），通过 `request.Bind[xxxRequest](c)` 绑定加校验；校验失败用 `request.InvalidParam("提示语")`。路由在 `internal/server/router.go` 的 `/api` 分组下注册。
- 新增配置项在 `internal/config` 的 `setDefaults` 里登记默认值（否则环境变量覆盖不生效），同时更新 `config.example.yaml` 和 README 的配置项表，需要时还有 `compose.yaml`。

## 前端

- 请求都经 `src/api/request.ts` 的 `request<T>()`；每个后端模块对应 `src/api/<module>.ts`，只放请求函数和手写的类型（与后端 camelCase JSON 对齐）。服务端要等较久的请求在 API 模块里用 `slowRequestTimeout` 放宽超时，界面上用 `useElapsed` 显示已用秒数；上传文件用 `uploadRequestTimeout`（不设超时）。
- 服务端数据一律用 TanStack Query：查询键与查询 hook 放在 `src/hooks/use-<资源>.ts`；键为列表 `['<资源>']`、详情 `['<资源>', id]`；`useMutation` 成功后 `invalidateQueries` 刷新。跨组件共享的客户端状态用 Zustand（`src/stores/`），局部状态用 `useState`。
- 操作反馈：成功用 toast；失败用 `components/ErrorNote` 显示在出错的位置，保留到下次操作或手动关闭（例外：立即补建被拒绝的 409 用 toast）。
- 删除这类不可恢复的操作先用 `components/ConfirmButton` 确认，确认框写明后果；确认后确认框随即关闭，进行中的状态和失败提示显示在页面上，不留在确认框里。
- UI 组件用 shadcn/ui（Base UI），经 `pnpm dlx shadcn@latest add` 添加到 `src/components/ui/`。

## 测试

- 后端 service 的测试写在领域包里（`package xxx_test`），用真实数据库加假适配器，不替换数据库访问，共用的辅助函数和假适配器在 `internal/testenv`；涉及后台 goroutine、定时器的用 synctest 气泡和假时间，不靠 sleep。写法见 `docs/architecture/testing.md`。
- 外部系统适配器的测试只回放 `testdata/` 里的样本，不联网。

## 文档

- 改动用户能看到的行为、配置项或部署方式时同步更新 `README.md`（只写中文）；开发相关的内容放 `CONTRIBUTING.md`。
- README 和其他文档里不写 B 站的接口地址和参数，只说"贴 B 站链接"。
- 改了 `docs/architecture/` 里描述的行为时同步更新对应的文档；业务语义以 `GLOSSARY.md` 和 ADR 为准。

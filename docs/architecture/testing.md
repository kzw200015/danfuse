# 测试的写法

各功能自己的测试环境写在对应的架构文档里（季绑定见 [`season-binding.md`](season-binding.md)，弹弹 API 见 [`dandan-api.md`](dandan-api.md)，外部系统的样本见 [`sources.md`](sources.md)、[`catalog.md`](catalog.md)）。

## 后端

### 数据库

- 数据库测试用真实的 PostgreSQL，基座是 `internal/database/dbtest`：测试包的 `TestMain` 里调用 `dbtest.Main(m)`（testcontainers 起一个 `postgres:18` 容器，跑一次迁移作为模板库），测试里 `pool := dbtest.Pool(t)` 拿到从模板复制出的独立库（可配合 `repository.NewStore(pool)`），测试之间互不干扰，可以 `t.Parallel()`。
- `-short` 时 `dbtest.Pool` 跳过当前测试。要自己创建连接池时用 `dbtest.Config(t)`，它建好库、登记删库，只返回连接配置。

### HTTP

- 在 `server` 包内用 `New(...)` 组装完整的 Echo，经 `httptest` 发请求；要换掉托管的前端文件时用 `newServer(..., fstest.MapFS{...})`。
- 共用的辅助函数在 `server_test.go`：`call` 发请求并解出统一响应，`assertAPIErrors` 逐条检查失败请求的状态码和提示，`assertJSON` 按语义比较 JSON，`popTime` 取走取决于当前时间的字段，`decodeLogEntry` 解出 5xx 日志，`newPool` 建连接池。
- HTTP 测试只管路由、参数绑定与校验、状态码、响应结构和错误映射；业务规则（级联删除、计数、状态变化、进度）由 service 的测试覆盖，不在这里重复。

### service

- 只用真实数据库加假适配器（实现领域包的接口，如 `catalog.Source`、`source.Adapter`），不替换 `repository.Store`。
- 并发用例让假适配器停在 channel 上（例如 `binding_test.go` 的 `fakeAdapter` 在 `started` 上报到、等 `release` 放行），期间直接执行 SQL（删除集等），再放行。
- `assertInvariants` 检查不变量：每个绑定的 `danmaku_count` 等于它实际的弹幕条数、`file_count` 等于它的弹幕文件份数，images 表里没有孤儿图片，处理过的记录都指向存在的集，带 `season_binding_id` 的绑定所在的集属于那个季绑定的季。绑定测试由 `newBindingService` 在每个用例结束时检查，气泡里的测试由 `syncTest` 检查。

### 后台 goroutine 与定时器（synctest）

- 涉及后台 goroutine、定时器的测试（`SyncService.Run`、季绑定）放在 `testing/synctest` 的气泡里，用 `synctest.Wait` 等后台停下、用假时间推进定时器，不靠 sleep。
- 连接池要在气泡里用 `dbtest.Config(t)` 新建、在气泡里关闭（pgx 连接内部的 channel 不能跨气泡使用），写法见 `internal/service/helpers_test.go` 的 `syncTest`。
- 气泡里的定时器和 `context.WithTimeout` 都用假时间，阻塞在数据库 I/O 上时假时间不前进、超时不会触发：数据库卡住时测试会一直挂到 `go test -timeout`。

### 外部系统的样本

- 外部系统适配器的测试用 `httptest` 回放 `testdata/` 里的真实样本，不联网；加 `-update` 时重新录制，`-update` 只作用于负责录制的那个用例，其他用例始终只回放。

## 前端

- 测试文件放在各目录的 `__tests__/` 下，命名 `*.spec.ts(x)`；jsdom 环境，未开启 globals，需从 `vitest` 显式 import。测试文件被 `tsconfig.app.json` 排除，由 `tsconfig.vitest.json` 单独做类型检查。
- 组件测试用 `vi.mock('@/api/<资源>')` 自动 mock 请求函数，并为每个用例新建 `QueryClient`；`request` 的测试通过替换 `http.defaults.adapter` 模拟响应。
- 涉及路由的测试用 `src/__tests__/utils.tsx` 的 `renderRoutes(path)`（`createMemoryRouter(routes)` 加新的 `QueryClient`）。根布局会取最近一次同步和设置，所以要 mock `@/api/sync`、`@/api/settings`，再调用同一文件的 `mockRootLayout()`。
- 同一文件里还有共用的 fixture 与 mock（`settings`、`binding`、`lighthouse`、`mockCatalog`、`syncRun`、`mockSyncRuns`、`seedSeries`、`card`），新用例先找这里。
- 涉及轮询的用例用 `vi.useFakeTimers({ shouldAdvanceTime: true })`，`vi.advanceTimersByTimeAsync` 推进轮询；点按钮前先等依赖的查询取到（按钮渲染出来时查询可能还没发出）。
- jsdom 缺少的 `matchMedia`、`scrollIntoView` 在 `vitest.setup.ts` 里补上。

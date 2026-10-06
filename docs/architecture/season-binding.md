# 季绑定与补建

在季上绑定一个合集，补建出普通的绑定（`service.SeasonBindingService`，ADR 0001）。术语见 `GLOSSARY.md`，合集与集号规则见 [`sources.md`](sources.md)。

## 数据

- 补建出的绑定由 `bindings.season_binding_id` 标明来源。
- 补建按弹幕源记住处理过的条目（`season_binding_handled`，只记它自己建出过绑定的，集删除时级联删除）；对应的集上已有同一个弹幕源的绑定（手动绑的、别的季绑定建的）时跳过，不拉取、不记处理过，条目表里是 `alreadyBound`。
- 条目表（`season_binding_items`）是上次检查时的合集内容，条目的状态读取时现算，不存储。
- 改集号规则时在同一个事务里按保存的标签重新认序号；补建拉取每个条目之前重新读它的序号（正在拉取的那个照旧按拉取前的序号写入）。

## 运行与追更

- `Run` 由 `App.Run` 和同步一起运行（循环见 [`runtime.md`](runtime.md)）：手动触发（创建、立即补建、改集号对应、打开追更）经它的循环拿到租约立即在后台开始，不同季绑定的补建互不等待。
- 追更的扫描由同一个循环每隔 `follow.scan_interval`（默认 1 分钟）在后台开始一次、一次补建一个，不等上一次扫描做完（同一个季绑定靠它的租约不会重复补建）。
- 到期的判定见 `ListDueSeasonBindings`：从没检查过、满一个检查周期 `follow.check_interval`（默认 12 小时）、季里有晚于上次检查建出的集、`follow.refetch_window`（默认 14 天）内建出的绑定在上次检查之后才满一个检查周期。拿到租约之后用同一条查询按 ID 再确认一次，列出之后才被检查过的跳过。
- "补建中"以按季绑定的租约为准（`leases` 表里有没有没过期的 `season_backfill:<ID>`），季绑定上不存运行状态。
- 一轮补建在事务之外列出合集、拉取弹幕，写入事务依次以 `FOR KEY SHARE` 锁住季、季绑定、集（先锁季：删季的级联先锁集、后锁季绑定，补建若先锁季绑定、后锁集就会与它死锁）；结束时把上次检查时间写为这一轮的开始时间，被关闭服务或丢失租约打断时不写。
- 追更时间规则是配置项（`config.Follow`，经 `NewSeasonBindingService` 传入，设置接口也返回它，前端的追更说明由 `lib/follow.ts` 按它写出）。与追更比较的时间（上次检查、绑定的建出与拉取时间）都取自应用的时钟（Go 的 `time.Now()`），不用数据库的 `now()`，测试才能用假时间推进；集的建出时间仍是数据库写入的。

## 管理界面

- 季绑定的详情（`use-season-bindings.ts`，键 `['season-bindings', id]`）只在展开条目表、或正在补建时取，正在补建时每秒轮询，结束后让剧详情失效；季绑定的列表在剧详情里。
- 创建、立即补建、改集号对应、开关追更成功后用 `useWatchSeasonBinding` 把最新的详情放进缓存，轮询随即开始。

## 测试

- 季绑定的测试都在 synctest 气泡里（`newSeasonEnv`：第 1 季的集按假时间写入建出时间，`SeasonBindingService.Run` 在后台运行），`synctest.Wait` 等后台的补建停下，`time.Sleep` 推进追更的扫描和 12 小时、14 天的边界（追更的时间规则用 `testFollow`，与配置的默认值一致）。
- `fakeCollector` 每次拉取在 `started` 上报弹幕源名字、从 `release` 收到一次放行；气泡与并发用例的通用写法见 [`testing.md`](testing.md)。

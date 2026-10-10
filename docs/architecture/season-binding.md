# 季绑定与补建

季绑定分两种，由 `season_bindings.kind` 区分（ADR 0008）：合集的季绑定（`collection`）在季上绑定一个合集，补建出普通的绑定（ADR 0001）；文件夹的季绑定（`folder`）由按季上传留下，只记着那次上传建出的文件绑定。两种都归 `seasonbinding.Service`，除了[文件夹的季绑定](#文件夹的季绑定)一节，下文说的都是合集的季绑定。术语见 `GLOSSARY.md`，合集与集号规则、按季上传见 [`sources.md`](sources.md)。

## 数据

- 补建出的绑定由 `bindings.season_binding_id` 标明来源。
- 补建按弹幕源记住处理过的条目（`season_binding_handled`，只记它自己建出过绑定的，集删除时级联删除）；对应的集上已有同一个弹幕源的绑定（手动绑的、别的季绑定建的）时跳过，不拉取、不记处理过，条目表里是 `alreadyBound`。
- 条目表（`season_binding_items`）是上次检查时的合集内容，条目的状态读取时现算，不存储。
- 改集号规则时在同一个事务里按保存的标签重新认序号；补建拉取每个条目之前重新读它的序号（正在拉取的那个照旧按拉取前的序号写入）。

## 文件夹的季绑定

- 只属于合集的季绑定的列（适配器、合集 ref、集号对应的两列、集号规则、按规则编号）对文件夹的季绑定为空，追更为关、未完结、状态为正常、没有上次检查和上次错误，由 CHECK 约束按 kind 守住；`title` 是所选的文件夹名。唯一约束 `(season_id, adapter, ref)` 在 NULL 上不生效，同一个文件夹可以传多次，每次各留下一个。
- 链接绑定只指向合集的季绑定、文件绑定只指向文件夹的季绑定，文件夹的季绑定没有条目表和处理过的记录：这些跨表的规则约束表达不了，由代码保证，`testenv.AssertInvariants` 检查。
- 只能查看和删除：`checkCollection` 让改追更、改集号对应或集号规则（PATCH）、立即补建对它返回 400"文件夹的季绑定只能删除"；追更恒为关，追更的扫描不会碰到它。`View` 的 `kind` 为 `folder`，合集专属的字段为 null，不经过源适配器；`running` 恒为 false，`createdAt` 即上传的时间；详情（`Get`）的条目表为空数组。
- 删除与合集的季绑定是同一条路径（`Delete`）：`withBindings` 时它建出的文件绑定连同原文件、弹幕级联删除，否则变回普通的文件绑定。建出的绑定被逐个删光时它照样保留，`bindingCount` 为 0。
- 由按季上传在写入事务里插入（`InsertFolderSeasonBinding`，要写明追更关着：列的默认值是开着），流程见 [`sources.md`](sources.md#按季上传)。

## 运行与追更

- `Run` 由 `App.Run` 和同步一起运行（循环见 [`runtime.md`](runtime.md)）：手动触发（创建、立即补建、改集号对应、打开追更）经它的循环拿到租约立即在后台开始，不同季绑定的补建互不等待。
- 立即补建是异步的（202，正在补建时 409），而创建绑定、重新拉取一个绑定是同步的：一轮补建的条目数不定、会遇到限流，放进请求里会超时，也需要租约防止同一个季绑定同时跑两轮；单个绑定的拉取有总时限（约 25 秒），结果（新增条数、失败原因）就是给用户的反馈，不另存状态。
- 追更的扫描由同一个循环每隔 `follow.scan_interval`（默认 1 分钟）在后台开始一次、一次补建一个，不等上一次扫描做完（同一个季绑定靠它的租约不会重复补建）。
- 到期的判定见 `ListDueSeasonBindings`：从没检查过、满一个检查周期 `follow.check_interval`（默认 12 小时）、季里有晚于上次检查建出的集。拿到租约之后用同一条查询按 ID 再确认一次，列出之后才被检查过的跳过。
- 补建只建出绑定，不重新拉取已经建出的绑定：之后的重新拉取是定时拉取（[`scheduled-fetch.md`](scheduled-fetch.md)），与追更开关、季绑定的租约都无关。
- "补建中"以按季绑定的租约为准（`leases` 表里有没有没过期的 `season_backfill:<ID>`），季绑定上不存运行状态。
- 一轮补建在事务之外列出合集、拉取弹幕，写入事务依次以 `FOR KEY SHARE` 锁住季、季绑定、集（先锁季：删季的级联先锁集、后锁季绑定，补建若先锁季绑定、后锁集就会与它死锁）。事务由季绑定开，锁住季和季绑定之后把 `pgx.Tx` 交给 `binding.Service.CreateBackfilledInTx`，由它锁集、插入绑定、写入弹幕，再记处理过；结束时把上次检查时间写为这一轮的开始时间，被关闭服务或丢失租约打断时不写。
- 追更时间规则是配置项（`config.Follow`，经 `seasonbinding.NewService` 传入，设置接口也返回它，前端的追更说明由 `lib/follow.ts` 按它写出）。与追更比较的时间（上次检查）和补建出的绑定的建出时间都取自应用的时钟（Go 的 `time.Now()`），不用数据库的 `now()`，测试才能用假时间推进；集的建出时间仍是数据库写入的。

## 管理界面

- 季绑定卡片（`SeasonBindingCard`）按 `kind` 分支，文件夹的季绑定的卡片只有文件夹名、建出的绑定数、上传时间和删除，不取详情也不轮询；前端的类型 `SeasonBinding` 是 `CollectionSeasonBinding | FolderSeasonBinding`。
- 季绑定的详情（`use-season-bindings.ts`，键 `['season-bindings', id]`）只在展开条目表、或正在补建时取，正在补建时每秒轮询，结束后让剧详情失效；季绑定的列表在剧详情里。
- 创建、立即补建、改集号对应、开关追更成功后用 `useWatchSeasonBinding` 把最新的详情放进缓存，轮询随即开始。

## 测试

- 季绑定的测试都在 synctest 气泡里（`newSeasonEnv`：第 1 季的集按假时间写入建出时间，`seasonbinding.Service.Run` 在后台运行），`synctest.Wait` 等后台的补建停下，`time.Sleep` 推进追更的扫描和 12 小时的边界（追更的时间规则用 `testFollow`，与配置的默认值一致）。
- `testenv.FakeCollector` 每次拉取在 `Started` 上报弹幕源名字、从 `Release` 收到一次放行；气泡与并发用例的通用写法见 [`testing.md`](testing.md)。

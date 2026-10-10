# 定时拉取

能重新拉取的绑定在建出后的一段时间内自动重新拉取（`binding.ScheduledFetchService`）。术语见 `GLOSSARY.md`：定时拉取以绑定为单位，不论绑定是手动建的还是补建出的，与追更无关；追更只管补建（[`season-binding.md`](season-binding.md)）。

## 规则

- 时间规则是配置项 `config.ScheduledFetch`（`scheduled_fetch.interval` 默认 12 小时、`scheduled_fetch.window` 默认 14 天，窗口为 0 时关闭），经 `binding.NewScheduledFetchService` 传入；设置接口也返回它，前端在设置弹出层里写出规则。
- 到期的判定见 `ListDueScheduledFetches`：`kind = 'link'`、建出时间在窗口内、上次尝试拉取（`bindings.fetch_attempted_at`）距现在已满一个间隔，按上次尝试拉取的时间从早到晚。
- `fetch_attempted_at` 由所有拉取写入，成功失败都算：创建、补建、拉取成功时与 `last_fetched_at` 一起写（`RecordFetch`），标为失效时同样（`MarkBindingDead`），其余上游错误（包括限流、超时）只写它（`RecordFetchAttempt`）。失败的绑定因此也要等满一个间隔才重试；手动重新拉取过的同样推后。服务器内部错误、关闭服务打断的拉取不算一次尝试。
- 失效的绑定照样定时拉取，拉取成功即恢复正常。
- 与定时拉取比较的时间（建出时间、上次尝试拉取的时间）都取自应用的时钟，不用数据库的 `now()`：`InsertLinkBinding` 由应用写入建出时间。

## 运行

- `Run` 由 `App.Run` 和同步、补建一起运行（见 [`runtime.md`](runtime.md)）。它没有手动触发，不用 `backgroundLoop`：每隔 `scheduledFetchScanInterval`（1 分钟，不设配置项）在自己的 goroutine 里扫描一次，一轮没做完时错过的扫描直接跳过。
- 每次扫描先列出到期的绑定，有到期的才拿全局的租约 `scheduled_fetch`（`database.LeaseScheduledFetch`，同追更的扫描、定时同步：先判断到期再拿租约），其他实例正在拉取时拿不到，跳过这次扫描；没有到期的绑定时不写租约表。
- 一轮里一个接一个地复用 `binding.Service.refetch`（只增不删），对平台的请求不并发；每个绑定拉取之前用同一条查询按 ID 再确认一次仍然到期（列出之后可能刚被手动重新拉取过、或已被删除），时间规则只写在 SQL 里。限流时结束这一轮，下一次扫描接着拉取剩下的。
- 手动重新拉取不经过租约：两边同时拉同一个绑定时各拉一次，写入由 `lockBinding` 排队，只增不删所以结果仍然正确。
- 有到期的绑定时，一轮结束记一条 `scheduled fetch finished`（到期几个、成功、失败、标为失效、新增弹幕条数、是否因限流结束），限流或出错时为 warn 级别；每个绑定的拉取另有 `danmaku fetched`。管理界面不显示进行中的状态。

## 测试

- 定时拉取的测试在 synctest 气泡里（`newFetchEnv`：两集，`ScheduledFetchService.Run` 在后台运行），用 SQL 写入绑定的建出与尝试拉取的时间，`time.Sleep` 推进扫描（`binding.ScheduledFetchScanInterval`，由 `export_test.go` 导出）和 12 小时、14 天的边界（时间规则用 `testScheduledFetch`，与配置的默认值一致）。适配器与季绑定的测试共用 `testenv.FakeCollector`，按名字记下拉取的顺序。

# 启动、后台任务与前端托管

## 启动流程

- `cmd/server/main.go` 依次做 配置加载 → 日志 → 连接数据库（`database.Connect`）→ 自动迁移（`database.Migrate`），再用 `app.New` 组装、`App.Run` 运行，不连目录源和 B 站。
- `App.Run` 用 errgroup 同时运行 HTTP 服务、后台同步（`SyncService.Run`）与季绑定的补建（`SeasonBindingService.Run`）。HTTP 服务出错（例如端口被占用）时同步与补建也随之退出。
- 两个 Run 的循环共用 `service/background.go` 的 `backgroundLoop`：定时与手动触发都在循环里处理，后台任务用循环的 ctx，循环返回前等它们结束。
- 定时同步的时间从同步记录算（最近一次同步的开始时间加 `sync.interval`，开始时间取自应用的时钟），不从进程启动算：重启不会推迟，启动时已经到期就立即同步，手动同步也会把下一次推后。多实例同时到点时只有拿到同步租约的那个同步，拿到租约之后还会再确认一次仍然到期（同追更的扫描），不会接连同步两次。追更同样按季绑定的上次检查时间判断是否到期，扫描本身按 `follow.scan_interval` 从启动开始计时。
- Run 等进行中的同步写完最终状态、补建停下才返回，之后 main 才关闭连接池（`defer pool.Close()`）。

## 迁移锁与租约

- 迁移文件通过 `db/embed.go` embed 进二进制，用 goose 自带的表锁（`goose_lock` 表里的租约）保证多实例只有一个执行迁移。
- 应用自己的锁是 `leases` 表里的租约（ADR 0003，`database.TryLease`，键集中登记在 `internal/database/lease.go`）：持锁期间不占用连接，后台每 10 秒续约、30 秒过期，丢失租约时取消 `Lease.Context()`（持锁做的事都用它），用完调用 `Release`。

## 前端托管

- `pnpm build` 直接输出到 `backend/web/static/dist`（不进 git），`backend/web/embed.go` 把它内嵌进二进制；没构建时内嵌的是提交进仓库的占位页 `static/placeholder/`，`go build`、`make run` 照常可用，管理界面只显示"前端未构建"。
- `server/frontend.go` 用 Echo 的 `middleware.StaticWithConfig`（`HTML5: true`）：文件存在就返回，路由和文件都没命中时回退到 `index.html`，直接刷新前端路由也能打开。
- `Skipper` 跳过 `/api/`、`/dandanplay/`，这两个前缀下没命中的路由仍按原来的方式返回 404。新增后端路由前缀时加进 `backendPrefixes`。
- 缓存头：`/assets/*` 长期缓存加 immutable，不存在的直接 404、不回退；其余 `no-cache`。
- 开发时用 `pnpm dev`，Vite 把 `/api`、`/dandanplay` 代理到后端。

# 目录：同步、海报与搜索

从目录源（目前只有 Jellyfin，`catalog/jellyfin` 实现 `catalog.Source`）同步出剧、季、集，同步核心在 `service.SyncService`。术语见 `GLOSSARY.md`。

## 同步与海报

- 目录源返回 `*catalog.Error`：同步页上的失败原因和海报警告只显示它的 Message。
- `series.poster_image_id` 不级联，顺序固定：同步换图为插入新图 → 剧指向新图 → 删除旧图；删除剧为先删剧、再删图：用 `DeleteSeries` 的 `RETURNING poster_image_id` 拿到海报 ID，在同一个事务里再删这张图，不先 SELECT。
- 图片接口 `GET /api/images/:id` 是统一响应的例外：成功时直接返回原始字节和存下的 content-type（`c.Blob`），加 `Cache-Control: public, max-age=31536000, immutable`（海报换了会换新的图片 ID）；出错时照常 return error，由 errorHandler 输出统一结构。前端用 `src/api/images.ts` 的 `imageUrl(id)` 交给 `<img>` 加载，不经过 `request()`。

## 管理界面的同步状态

- 根布局调用一次 `useLatestSyncRun`（`src/hooks/use-sync-runs.ts`），一直每 2 秒轮询最近一次同步（`GET /api/sync-runs/latest`），看到更新的一次同步结束后让剧列表和剧详情（`seriesKeys.list` 前缀）失效。
- 同步列表只在同步页每 2 秒轮询，选中的那次还在运行时它的详情也每 2 秒轮询。
- 手动触发同步在同步开始后返回 202 和它的 ID（toast 提示"已开始同步 #N"）；已有同步在跑时为 409"同步正在进行"，显示在按钮下方。

## 搜索列

- `fulltext` 包在 Go 里生成 tsvector、tsquery 的文本（NFKC + 小写，中日韩字符串二元切分），不经过 PostgreSQL 的分词器。
- `catalog.SearchVector` 拼出一季的搜索列，同步写入一部剧时在同一个事务里重算它所有季的搜索列。搜索列里季号只以数字出现在最后，不挪动剧名、原名的位置。
- 分词规则或搜索列的组成改变时，已有的搜索列靠 goose 的 Go 迁移重算，不设版本列。目前还没有 Go 迁移，第一次需要时：
  1. 在 `backend/db/migrations` 下按序号新增 `000NN_xxx.go`（`package migrations`），goose 从注册它的文件名取版本号。
  2. 在 `init` 里用 `goose.AddMigrationContext` 注册一个函数，在迁移的事务里读出所有季和所属的剧，用 `catalog.SearchVector` 算出搜索列写回。
  3. 在 `database` 包（`migrate.go`）空导入 `db/migrations`，Go 迁移与 SQL 迁移按版本号一起执行。

## 名称里的季号、集号

- 搜索关键词和弹弹 API `match` 的文件名都用 `catalog.ParseName` 按一张按优先级排列的正则表（`namePatterns`，命名捕获组 `season`、`episode`）认出写明了的季号、集号（`S01E11`、`S01`、`第2季`、`特别篇`、`第11话`、`EP11`），按季号、集号在 SQL 里精确过滤。
- 没有标注的数字（"剧名2"、"Mob Psycho 100"）分不出是标题的一部分还是季号，不拆，交给全文搜索。
- `SeasonName` 输出的名称（"剧名 第2季""剧名 特别篇"）能被 `ParseName` 拆回剧名和季号。

## Jellyfin 适配器的测试

- 用 `httptest` 回放 `testdata/` 里的真实样本；加 `-update` 时从 `e2e/` 环境重新抓取：`go test ./internal/catalog/jellyfin -run TestListSamples -update`（需要先按 `e2e/README.md` 搭好环境）。
- JSON 样本按"路径 + ParentId"命名，海报样本按条目 Id 存成原始图片 `image-<Id>.jpg`/`.png`，回放时 Content-Type 按内容识别。

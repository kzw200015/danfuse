# 导出

把绑定保存的弹幕按偏移校正后写成 B 站格式的 XML，可以导出一个绑定、一集或一季，一集和一季打成 zip。术语见 `GLOSSARY.md` 的"导出"：不做跨源去重、不屏蔽，不保证原样传回。

## 接口与分工

- 三个接口在 `internal/export/handler/routes.go` 注册，挂在 `/api` 下，成功时直接返回文件：
  - `GET /api/bindings/:id/export` → `application/xml`，`<绑定标题>.xml`
  - `GET /api/episodes/:id/export` → `application/zip`，`<剧名> S01E03.zip`（电影为 `<片名>.zip`），里面平铺各绑定的 `<绑定标题>.xml`
  - `GET /api/seasons/:id/export` → `application/zip`，`<剧名> S01.zip`，里面是 `<剧名> S01/S01E03/<绑定标题>.xml`
- 和图片接口一样是统一响应的例外：成功时不走 `response` 包；写出响应头之前的错误照常 return error，由 errorHandler 输出统一结构。ID 不是正整数时 400（`绑定 ID 不合法` 等，不是数字时 `请求参数错误`），不存在时 404（`绑定不存在`、`集不存在`、`季不存在`）。一集、一季没有可导出的弹幕时也是 404（`这一集没有可导出的弹幕`、`这一季没有可导出的弹幕`），不输出空的 zip；前端此时本该禁用按钮，剧详情加载之后绑定被删、被重新拉取时仍可能请求到。单绑定导出没有弹幕时输出一份空的 XML（只有 `<i>` 根节点）。
- `export` 是一个领域，和 `dandan` 并列，都是"出口"：dandan 是播放器协议，导出是文件。它不调用任何领域的 `Service`，剧、季、集、绑定与弹幕都由自己 `dao/` 里的查询读（`binding.sql`、`episode.sql`、`season.sql`，与 service 的功能文件同名），对所有表只读。dandan 经过 `binding.Service.EpisodeTracks` 是为了靠源适配器的注册表算出平台，导出用不到平台。
- service 的三个方法 `ExportBinding`、`ExportEpisode`、`ExportSeason` 都接收一个 `service.Open`（`func(filename string) io.Writer`）：查找与校验都通过、写出任何内容之前恰好调用一次，给出文件名，拿到写内容的 `io.Writer`；出错时不调用。用回调而不是"先返回文件名、再另调一次写内容"：全部读取要在 service 开、关的同一个事务里（见下面的快照），文件名与写出之间正好隔着 handler 输出响应头这一步。
- handler 的 `download`（`download.go`）实现这个回调：`open` 设好 `Content-Type` 与 `Content-Disposition`、显式 `WriteHeader(200)`，记下响应头已经输出；`finish` 处理 service 返回的错误（见下面的流式输出）。`Content-Disposition` 为 `attachment`，同时给 ASCII 的 `filename` 兜底（可打印 ASCII 以外的字符和 `"`、`\` 换成 `_`）与 RFC 5987 的 `filename*=UTF-8''…`（attr-char 以外的字节按 `%XX` 编码；不用 `url.PathEscape`，它保留的 `'` 正是 `filename*` 的分隔符）。

## 内容规则

- 每份 XML 是一个绑定落库的全部弹幕（`bindingDanmaku`）：按绑定的偏移校正，用 `danmaku.Correct`（与弹弹 API 的 `danmaku.Merge` 共用的同一条规则：`t + offset` 四舍五入到毫秒，为负的丢弃，超出本集时长的保留），按校正后的时间升序、同一时间按原始 ID 排序。不做跨源去重，不屏蔽。
- 范围与弹弹 API 读取一集的相同：失效的绑定、季绑定建出的绑定都导出。
- 集、季的 zip 跳过没有弹幕的绑定，按校正之后的条数判断：校正后全部丢弃的也跳过；季的 zip 里没有可导出绑定的集不出现。zip 里不写目录项，目录只出现在文件的路径里。
- 集按集号排序（`ListEpisodesBySeason`），一集里的绑定按 ID 即创建顺序（`ListBindingsByEpisode`），重名后缀按这个顺序编号，所以是稳定的。
- XML 由 `bilifmt.Encode`（`danmaku/bilifmt`，与 `Decode` 放在一起，是它的逆过程）写出：XML 声明、`<i>` 根节点，每条 `<d p="时间,模式,25,颜色,0,0,,原始 ID,0">正文</d>`；时间为秒、精确到毫秒，模式、颜色（十进制 RGB）、原始 ID 用保存的值，其余是固定的默认值。正文只转义 `& < > "`，不做其他处理。
- 入库时 `bilifmt.NewDanmaku` 已去掉 XML 1.0 不允许的字符（见 [`sources.md`](sources.md#弹幕文件adr-0004)）。库里在这之前存下的弹幕不清洗、不写迁移，导出时原样写出，严格的 XML 解析器可能打不开，是接受的代价。

## 命名

规则都在 `service/filename.go`：

- XML 的文件名是绑定标题加 `.xml`，标题为空（或只有空白）时用 `弹幕`。文件名就是标题，所以任何一份 XML 在集面板上传回去时绑定标题不变。
- 文件名、目录名里的 `/ \ : * ? " < > |` 与控制字符（`unicode.IsControl`）换成 `_`；名称部分（扩展名和重名后缀之前）超过 200 字节（`maxNameBytes`，给常见文件系统 255 字节的上限留出余量）时截断，不切开 UTF-8 字符。
- 同一目录下重名时按创建顺序依次加 ` (2)`、` (3)`，直到没用过（标题本身就是 `OP (2)` 的绑定，名称被占了时用 `OP (2) (2)`）。
- 季号、集号补到两位（`S01E03`，特别篇 `S00`），超过两位照常写（`E100`）。季 zip 的子目录只写 `S01E03`，不带集标题（集标题可能为空，也可能带数字干扰集号规则），所以解压出的根目录可以直接用于按季上传，子目录名能被默认集号规则认出；但按季上传会把一集的几份 XML 合成一个绑定。
- 电影（剧的类型为 movie）的集导出不写季号集号；电影在界面上没有季面板、没有季导出的入口，接口本身不区分剧的类型。

## 快照与流式输出

- 一次导出的全部读取在一个只读的 REPEATABLE READ 事务里（`service.go` 的 `snapshot`，`pgx.BeginTxFunc`）：导出途中正好有绑定在重新拉取或被删除时，zip 里仍是同一时刻的内容。这是隔离级别默认用 READ COMMITTED 的例外，只读、不加锁，不与写入事务冲突。PostgreSQL 在事务的第一条语句取快照，所以查找与校验也在事务里做。
- 集、季的 zip 边读边写（`archive.go`）：`archive` 第一次加文件时才调用 `open`，所以没有可导出的弹幕时一个字节都不写出、service 返回 404；`archive/zip` 的 `Create` 用 Deflate，按绑定逐个读取弹幕、逐个写出，内存只占一个绑定的量。`open` 因此是在事务里调用的。
- 单绑定的导出先在事务里读完全部弹幕（排序要全部读出来），事务结束后才调用 `open` 写出：读取出错时还能照常返回错误响应。
- 响应头输出之后出错时（只有集、季的 zip 会遇到），响应已改不成错误（errorHandler 遇到已提交的响应什么也不做）：`download.finish` 不写 zip 的中央目录，经 `response.LogServerError` 记 5xx 日志（请求的 ctx 已取消时按约定记 info 级别的 `request canceled`），再 `panic(http.ErrAbortHandler)`，Recover 中间件把它原样抛给 net/http 中止连接，浏览器把下载标为失败，而不是存下一份看着正常、实际缺了几集的文件。

## 前端

- 导出的地址在 `src/api/export.ts`（`bindingExportUrl`、`episodeExportUrl`、`seasonExportUrl`），交给 `views/catalog/ExportButton.tsx` 的 `<a href download>` 由浏览器直接下载，不经过 `request()`、不把文件读进内存；单独成模块，是为了组件测试里 `vi.mock('@/api/bindings')` 不会把它们一起替换掉。
- "导出 XML"按钮在绑定卡片（`BindingCard`）、集面板右上角（`EpisodePanel`，与绑定链接、上传弹幕文件同一处）和季面板右上角（`SeasonPanel`）。禁用条件按剧详情里已有的 `danmakuCount` 判断（`catalog.ts` 的 `hasDanmaku`），不另发请求：绑定卡片不禁用（失效、没有弹幕的照常可点）；集面板在这一集没有 `danmakuCount > 0` 的绑定时禁用，季面板在整季都没有时禁用。链接没有禁用状态，所以禁用时换成一个禁用的 `Button`。电影没有季面板，只有绑定卡片和集面板的按钮。

## 测试

- service 的测试（`internal/export/service/*_test.go`）是主切入点：真实数据库加 `testenv`，绑定与弹幕用 SQL 写入（`helpers_test.go` 的 `insertEpisodeBinding`、`insertLargeBinding`），把产出的 zip、XML 读回来（`archive/zip` 加 `bilifmt.Decode`，`exportZip`、`readZip`、`decodeXML`）再比较，不断言内部的函数；`exportZip` 同时检查文件名恰好给出一次、内容写在给出文件名之后。命名、校正等规则都在这一层测，不另写纯函数的测试，`danmaku.Correct` 也由这里和 `Merge` 的测试覆盖。
- 快照的并发用例 `TestExportSeasonSnapshot`：第一集的弹幕多到超过 zip 的写缓冲，写这一集时就开始写出，`gatedWriter` 在第一次 `Write` 时停住，期间另一个事务删掉第二集的绑定，放行后断言 zip 里仍是删除前的内容（同 `binding/service/helpers_test.go` 让假适配器停在 channel 上的写法）。
- HTTP 测试在 `internal/server/danmaku_export_test.go`（不叫 `export_test.go`：那个名字约定给测试导出内部符号用）：`Content-Type`、带 `filename*` 的 `Content-Disposition`（用 `mime.ParseMediaType` 解回文件名）、400、404 的统一错误。`TestExportSeasonAbort` 用 `httptest.NewServer` 起真实的服务，连接池上挂 `breakingTracer`（第 n 次执行某条 sqlc 查询前关掉数据库连接），断言拿到 200 和一部分 zip、读响应体时 `io.ErrUnexpectedEOF`、zip 解不开，且记了一条 5xx 日志。
- `bilifmt_test.go` 的 `TestEncode` 覆盖 `Encode` → `Decode` 的往返（转义的正文、各模式、颜色、毫秒时间），`TestDecode` 覆盖入库时去掉 XML 不允许的字符、去掉后为空的丢弃。
- 前端的组件测试在 `views/__tests__/CatalogView.spec.tsx`（绑定卡片、集面板）和 `SeasonBindings.spec.tsx`（季面板、电影没有季的按钮）：断言 `href` 指向导出接口和禁用条件。

# 弹幕源与绑定

源适配器、合集与集号规则、弹幕文件，以及管理界面的绑定卡片。术语见 `GLOSSARY.md`，相关决策见 ADR 0004、0005。

## 源适配器

- 绑定存的是适配器 ID 加适配器自己的 ref（jsonb），ref 只交给适配器解析：绑定 JSON 里的 `sourceUrl`/`sourceLabel` 一律经适配器的 `Describe` 生成（`binding.Service` 的 `view`），原始 ref 不对外输出。
- 源适配器只有 `source.Adapter` 一个接口，一个平台一个实现，方法都要实现，业务代码不做类型断言：
  - 弹幕源：`ParseLink`（集面板贴链接得到弹幕源 ref）、`Describe`、`Fetch`。
  - 合集：`ParseCollectionLink`（识别季面板的链接给出候选）、`ListCollection`（按合集 ref 列出条目）、`DescribeCollection`（生成展示的链接和标签）；没有合集的平台 `ParseCollectionLink` 一律返回 `source.ErrUnrecognized`。
  - 它只适用于按 ref 能重新拉取的弹幕源，弹幕文件不实现它。
- 合集 ref（`source.CollectionRef`）与弹幕源 ref 是两种类型；合集条目的弹幕源 ref 与单集绑定同一套格式，所以手动绑过的同一个弹幕源能被认出来。
- 集号对应（`source.Mapping`）、集号规则（`source.EpisodeRule`）、条目的整理（`source.NumberItems`）是 `source` 包里的纯计算，不在适配器里做：
  - 集号规则（ADR 0005）：投稿合集与多 P 投稿的适配器只给出展开到分 P 的条目和标签、标明 `Collection.NumberedByRule`，序号由季绑定上的规则从标签认出。规则是一组正则，取标签里结束得最靠后的集号、一样靠后时取排在前面的正则（`naming.Parse` 仍按表的优先级认，两者只共用正则），季绑定存它的副本；默认规则取自 `naming.EpisodePatterns`（`catalog/naming`），即 `naming.Parse` 那张表里带 `episode` 组的写法，再加上认结尾 `/ N` 的一条：标签有两级标题时用 `source.LabelSeparator`（` / `）拼成"上级 / 下级"，越靠后越具体（见 `CollectionItem.Label`），新的源适配器拼标签也要照这个约定。
  - `NumberItems` 先按 ref 去重（`uniqueRefs`），按规则编号的合集再认出序号，最后标出重复序号（`markDuplicateNumbers`）；`NumberLabels` 只接收名称，认序号、标重复，不去重，结果与名称一一对应（按季上传的预览用它）。
- B 站适配器的链接解析（`bilibili/link.go`）只做字符串分类（短链先跳转一次再分类），集面板与季面板各自决定接受哪些（集面板遇到合集的链接时提示到季面板）。
- 源适配器返回 `*source.Error`（带 Kind）：补建、定时拉取、标为失效都按 Kind 分支；管理 API 由 `binding.SourceAPIError`（季绑定的预览、创建也用它）按 Kind 转成 400/422/502，只包装 Err，日志里提示不重复。

## 弹幕文件（ADR 0004）

- 上传的 B 站 XML 弹幕文件建绑定，不实现 `source.Adapter`。`danmakufile` 领域包只做解析（XML 的解码与字段映射在 `danmaku/bilifmt`，B 站适配器共用）；业务在 `internal/binding/file.go`（创建、按季上传的解析与写入、追加文件、重新解析、文件列表）。
- 绑定的 `kind` 区分 `link` / `file`：文件绑定的 `adapter`、`ref`、`duration` 为空（CHECK 约束守住，唯一约束因此只作用于链接绑定）。
- 原文件存 `binding_files`（按 sha256 在绑定内去重），份数 `file_count` 与 `danmaku_count` 一样在事务里维护；弹幕不属于任何平台，原始 ID 直接用 dmid。
- `binding.Service` 的 `view`、`platform` 按 kind 分支，只对一种绑定有效的操作用在另一种上时返回 400。
- 上传的 handler 先调用 `request.ReadFiles`（`internal/httpx/request`，单集上传和按季上传共用，上限经 `request.FileLimits` 传入），然后才 `request.Bind`：它给请求体套上 `http.MaxBytesReader`，用 `MultipartReader` 逐个 part 流式读取，边读边查份数、单份和合计大小，不落临时文件，也没有 `ParseMultipartForm` 的 1000 个 part 上限；文件以外的字段（合计最多 1 MB）读完后放回 `Request.Form`，所以之后的 `Bind` 照常可用。文件名取请求里原样的 filename，不像 `Part.FileName` 那样去掉目录（按季上传用它传相对路径）；单集上传自己再取 base name。

### 按季上传

- 每次按季上传留下一个文件夹的季绑定（ADR 0008，见 [`season-binding.md`](season-binding.md#文件夹的季绑定)），建出的文件绑定带着它的 `season_binding_id`，所以归 `seasonbinding`（领域的依赖方向是 seasonbinding → binding）：两条接口的 handler（`seasonbinding/handler_season_upload.go`）和 service（`season_upload.go`）都在那里，做法照补建。弹幕文件的部分仍在 `binding`，与单集上传共用文件类型 `UploadedFile`（按季上传时 `Name` 是相对路径）、解析（`parseFiles`）、存原文件与写弹幕（`addFiles`，文件名取 base name），导出给 seasonbinding 的是 `UploadedFile`、`SeasonEntry`、`Service.ParseSeasonFiles`、`Service.CreateSeasonFilesInTx`。
- 预览 `POST /api/seasons/:id/file-bindings/preview`（JSON `{labels, episodePatterns}`，`seasonbinding.Service.PreviewSeasonUpload`）：把条目名称交给 `source.NumberLabels`，与季绑定预览同一套匹配、NFKC 回退和"集号重复"的标注，规则的校验也一样（`source.ParseEpisodeRule`）。返回 `{items: [{label, number, reason}]}`，顺序与 `labels` 相同。只读；集号对应和对到哪一集由前端现算。
- 创建 `POST /api/seasons/:id/file-bindings`（multipart，`seasonbinding.Service.CreateFromSeasonFiles`）：`files` 多份，每份的文件名是以所选文件夹名开头的相对路径（前端 `FormData.append` 时传入）；`targets` 是一个 JSON 数组字段，每个条目一项 `{label, episodeId}`，`episodeId` 取自预览。
- seasonbinding 的 handler 用 `request.ReadFiles` 读出文件，按文件名分成条目、配上目标集（`seasonEntries`、`groupSeasonFiles`），文件夹名即文件夹的季绑定的名称。分组与校验只看请求本身就能判断，属于 handler（"两个条目对到同一集"在请求的 `Validate`，分组要等文件读出来，在 Bind 之后）；路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"，扩展名不分大小写，文件夹名都相同；子目录合成一个条目"文件夹名 / 子目录名"，顶层的文件各自一个条目"文件夹名 / 文件名去掉扩展名"，名称不能重复；条目与 `targets` 一一对应，两个条目不能对到同一集。有一项不满足就整次 400。前端的 `groupFolderFiles`（`season-upload.ts`）按同样的规则先分组，另外忽略不是 `.xml` 的文件。
- service 先在事务外由 `binding.Service.ParseSeasonFiles` 解析全部文件，有一份认不出就整次 422，提示用它的相对路径，存下的文件名取 base name。再在一个事务里锁住季、插入文件夹的季绑定（`InsertFolderSeasonBinding`），把 `pgx.Tx` 和季绑定 ID 交给 `binding.Service.CreateSeasonFilesInTx`，由它先用一条查询锁住全部目标集（`LockSeasonEpisodes` 同时确认它们属于这一季，有一集不是就整次 404、提示重新预览，还没写入任何内容），再逐个插入带 `season_binding_id` 的文件绑定（标题为条目名称）、`addFiles`。加锁顺序与补建相同（先锁季）；失败时季绑定和绑定都不保存。目标集上已有绑定（包括文件绑定）时照常再建一个。返回 201 `{bindings, added}`，记一条带季绑定 ID 的 info 日志。
- 份数与合计大小另有上限 `danmaku_file.season_max_files` / `season_max_upload_mb`，单份仍是 `max_file_mb`。这个请求是同步的，前端不设超时（`uploadRequestTimeout`，单集上传、追加文件也是）；HTTP 服务也不限整个请求的读写时长，只限制读请求头和空闲连接（见 `server.go`）。

## 管理界面

- 创建绑定、重新拉取由后端当场拉取（最长约 25 秒），`bindings.ts` 里用 `slowRequestTimeout` 放宽超时，界面上用 `useElapsed` 显示已用秒数。
- 绑定卡片（`BindingCard`）按 `kind` 分出两组操作（`BindingSourceActions.tsx`：重新拉取 / 追加文件、重新解析），各自持有 mutation；一个绑定上改动弹幕的变更都带 `bindingKeys.write(id)` 前缀（`use-bindings.ts`），卡片用 `useIsMutating` 在任何一个进行中时禁用其他操作。
- 季绑定建出的绑定（`seasonBindingId` 不为空）在卡片上带一个"季绑定 · 名称"的标签，只用来显示、不是链接：集面板把剧详情里这一季的 `seasonBindings` 交给 `BindingCard`，由 `seasonBindingTag`（`season-binding.ts`）按 ID 查出季绑定，名称取法与季绑定卡片一致（`seasonBindingName`：文件夹的季绑定为文件夹名，合集的季绑定为合集标题、为空时用合集的标签），过长时截断，悬停提示写全名和来源；查不到时只写"季绑定"。
- 弹幕文件列表的键 `['binding-files', id]`，弹出层打开时才取。
- 按季上传的对话框（`SeasonUploadButton`，入口在季面板"季绑定"一节的标题行）复用季绑定的 `EpisodeRuleRepreview`（集号规则与重新预览）、`MappingInputs`、`previewTarget` 的现算和 `CollectionItemsTable`（传 `selection` 加勾选列）；集号对应和季绑定一样预填 1 = 1。"已有文件绑定"看目标集的绑定里有没有 `kind` 为 `file` 的，有就默认不勾。上传显示 axios 的上传进度，传完显示"正在保存"；成功后让剧详情失效（新的文件夹的季绑定随之出现），失败时对话框保持原样。
- 查看绑定的弹幕（`BindingDanmakuDialog`，点卡片上的弹幕条数打开）：`GET /api/bindings/:id/danmaku?fromMs=&after=` 按 `(time_ms, source_id)` 游标分页，每页 200 条，时间未校正，不输出原始 ID（可能超出 JS 的安全整数）；`fromMs` 用于跳转，翻页时照传。绑定 JSON 带着 `contentVersion`（插入了新弹幕或替换全部弹幕时加 1）和 `maxTimeMs`（最晚一条弹幕的时间，拖动条的长度），两者与 `danmaku_count` 一样在写入弹幕的事务里维护。前端的无限查询键是 `['binding-danmaku', id, contentVersion, fromMs]`：不论弹幕怎么变的，剧详情重新加载后版本一变就换一份数据，同一个键不会过时，不重新请求。

## B 站适配器的测试

- 平时只回放 `internal/source/bilibili/testdata/` 里脱敏后的样本。测试里假 B 站换掉的是适配器 `http.Client` 的 Transport：各个域名（API、XML 弹幕、短链）的请求都转给它，按 Host 区分接口。
- live 模式请求真实的 B 站，默认关闭，CI 不请求：`go test ./internal/source/bilibili -run TestLive -args -live` 只验证；再加 `-update` 时先清空 `testdata/`，把响应脱敏后写进去（原始响应不落盘，限流、接口异常这类出错的响应不录制）。
- 在改动 B 站适配器之后和里程碑验收时各跑一遍，全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速；以未登录的身份请求，港澳台限定番剧的用例要求从大陆的网络请求。
- 固定的公开视频列表（集面板的单集、季面板的合集）、脱敏规则见 `live_test.go` 开头，列表只挑内容中性的视频和番剧，状态变了（包括列表里连载中的番剧完结了）就换一个再重新录制。

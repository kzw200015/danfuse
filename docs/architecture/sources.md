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
  - `NumberItems` 按规则认出序号，再交给 `normalizeItems` 按 ref 去重、标出重复序号。
- B 站适配器的链接解析（`bilibili/link.go`）只做字符串分类（短链先跳转一次再分类），集面板与季面板各自决定接受哪些（集面板遇到合集的链接时提示到季面板）。
- 源适配器返回 `*source.Error`（带 Kind）：补建、定时拉取、标为失效都按 Kind 分支；管理 API 由 `binding.SourceAPIError`（季绑定的预览、创建也用它）按 Kind 转成 400/422/502，只包装 Err，日志里提示不重复。

## 弹幕文件（ADR 0004）

- 上传的 B 站 XML 弹幕文件建绑定，不实现 `source.Adapter`。`danmakufile` 领域包只做解析（XML 的解码与字段映射在 `danmaku/bilifmt`，B 站适配器共用）；业务在 `internal/binding/file.go`（创建、按季上传、追加文件、重新解析、文件列表）。
- 绑定的 `kind` 区分 `link` / `file`：文件绑定的 `adapter`、`ref`、`duration` 为空（CHECK 约束守住，唯一约束因此只作用于链接绑定）。
- 原文件存 `binding_files`（按 sha256 在绑定内去重），份数 `file_count` 与 `danmaku_count` 一样在事务里维护；弹幕不属于任何平台，原始 ID 直接用 dmid。
- `binding.Service` 的 `view`、`platform` 按 kind 分支，只对一种绑定有效的操作用在另一种上时返回 400。
- 上传的 handler 先给请求体套 `http.MaxBytesReader` 再解析 multipart，然后才 `request.Bind`：`parseUpload`（套上限、解析、查份数）与 `readUploads`（查单份与合计大小、读出内容）由单集上传和按季上传共用，上限经 `uploadLimits` 传入。

### 按季上传

- 归 `binding` 而不是 `seasonbinding`：建出的是普通的文件绑定，与单集上传共用文件类型 `UploadedFile`（按季上传时 `Name` 是相对路径）、解析（`parseFiles`）、存原文件与写弹幕（`addFiles`，文件名取 base name）。不改季绑定的任何表，也不留下"这批绑定来自同一次上传"的记录。
- 预览 `POST /api/seasons/:id/file-bindings/preview`（JSON `{labels, episodePatterns}`，`PreviewSeasonUpload`）：把条目名称当作按规则编号的合集的标签交给 `source.NumberItems`（ref 取下标，只为让条目互不相同），与季绑定预览同一套匹配、NFKC 回退和"集号重复"的标注，规则的校验也一样（`source.ParseEpisodeRule`）。返回 `{items: [{label, number, reason}]}`，顺序与 `labels` 相同。只读；集号对应和对到哪一集由前端现算。
- 创建 `POST /api/seasons/:id/file-bindings`（multipart，`CreateFromSeasonFiles`）：`files` 多份；`paths` 与 `targets` 各是一个 JSON 数组字段。`paths` 与 `files` 一一对应、顺序相同，是以所选文件夹名开头的相对路径：Go 的 multipart 会把上传文件名裁成 base name，所以另传；逐份一个字段时 500 份文件就超过 `multipart.ReadForm` 默认 1000 个 part 的上限，所以合成一个字段。`targets` 每个条目一项 `{label, episodeId}`，`episodeId` 取自预览。
- 分组与校验在请求的 `Validate`（`groupSeasonPaths`）：只看请求本身就能判断，属于 handler；路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"，扩展名不分大小写，文件夹名都相同；子目录合成一个条目"文件夹名 / 子目录名"，顶层的文件各自一个条目"文件夹名 / 文件名去掉扩展名"，名称不能重复；条目与 `targets` 一一对应。有一项不满足就整次 400。前端的 `groupFolderFiles`（`season-upload.ts`）按同样的规则先分组，另外忽略不是 `.xml` 的文件。
- service 先在事务外解析全部文件，有一份认不出就整次 422，提示用它的相对路径，存下的文件名取 base name；两个条目对到同一集时 400。再在一个事务里锁住季、逐个锁住目标集（`LockSeasonEpisode` 同时确认它属于这一季，否则整次 404、提示重新预览）、插入文件绑定（标题为条目名称）、`addFiles`。目标集上已有绑定（包括文件绑定）时照常再建一个。返回 201 `{bindings, added}`，记一条 info 日志。
- 份数与合计大小另有上限 `danmaku_file.season_max_files` / `season_max_upload_mb`，单份仍是 `max_file_mb`。加上 `paths`、`targets` 两个字段不能超过 1000 个 part（超过时只能报"请求参数错误"），所以启动时校验 `season_max_files` 不超过 998、`max_files` 不超过 1000。这个请求是同步的，前端不设超时（`timeout: 0`），时长以服务端的 `server.read_timeout` / `write_timeout` 为准；Go 的 `WriteTimeout` 从读完请求头起算，包括接收请求体的时间，所以两个都要容得下整次上传。

## 管理界面

- 创建绑定、重新拉取由后端当场拉取（最长约 25 秒），`bindings.ts` 里用 `slowRequestTimeout` 放宽超时，界面上用 `useElapsed` 显示已用秒数。
- 绑定卡片（`BindingCard`）按 `kind` 分出两组操作（`BindingSourceActions.tsx`：重新拉取 / 追加文件、重新解析），各自持有 mutation；一个绑定上改动弹幕的变更都带 `bindingKeys.write(id)` 前缀（`use-bindings.ts`），卡片用 `useIsMutating` 在任何一个进行中时禁用其他操作。
- 弹幕文件列表的键 `['binding-files', id]`，弹出层打开时才取。
- 按季上传的对话框（`SeasonUploadButton`，入口在季面板"季绑定"一节的标题行）复用季绑定的 `EpisodeRuleRepreview`（集号规则与重新预览）、`MappingInputs`、`previewTarget` 的现算和 `CollectionItemsTable`（传 `selection` 加勾选列）；集号对应预填 `defaultMapping`（最小的集号在本季有同号的集时同号对应，否则对到本季最小的集号），季绑定仍预填 1 = 1。"已有文件绑定"看目标集的绑定里有没有 `kind` 为 `file` 的，有就默认不勾。上传显示 axios 的上传进度，传完显示"正在保存"；成功后让剧详情失效，失败时对话框保持原样。
- 查看绑定的弹幕（`BindingDanmakuDialog`，点卡片上的弹幕条数打开）：`GET /api/bindings/:id/danmaku?fromMs=&after=` 按 `(time_ms, source_id)` 游标分页，每页 200 条，时间未校正，不输出原始 ID（可能超出 JS 的安全整数）；`fromMs` 用于跳转，翻页时照传。绑定 JSON 带着 `contentVersion`（插入了新弹幕或替换全部弹幕时加 1）和 `maxTimeMs`（最晚一条弹幕的时间，拖动条的长度），两者与 `danmaku_count` 一样在写入弹幕的事务里维护。前端的无限查询键是 `['binding-danmaku', id, contentVersion, fromMs]`：不论弹幕怎么变的，剧详情重新加载后版本一变就换一份数据，同一个键不会过时，不重新请求。

## B 站适配器的测试

- 平时只回放 `internal/source/bilibili/testdata/` 里脱敏后的样本。测试里假 B 站换掉的是适配器 `http.Client` 的 Transport：各个域名（API、XML 弹幕、短链）的请求都转给它，按 Host 区分接口。
- live 模式请求真实的 B 站，默认关闭，CI 不请求：`go test ./internal/source/bilibili -run TestLive -args -live` 只验证；再加 `-update` 时先清空 `testdata/`，把响应脱敏后写进去（原始响应不落盘，限流、接口异常这类出错的响应不录制）。
- 在改动 B 站适配器之后和里程碑验收时各跑一遍，全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速；以未登录的身份请求，港澳台限定番剧的用例要求从大陆的网络请求。
- 固定的公开视频列表（集面板的单集、季面板的合集）、脱敏规则见 `live_test.go` 开头，列表只挑内容中性的视频和番剧，状态变了（包括列表里连载中的番剧完结了）就换一个再重新录制。

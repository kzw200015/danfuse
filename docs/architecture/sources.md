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
  - 集号规则（ADR 0005）：投稿合集与多 P 投稿的适配器只给出展开到分 P 的条目和标签、标明 `Collection.NumberedByRule`，序号由季绑定上的规则从标签认出。规则是一组正则，取标签里结束得最靠后的集号、一样靠后时取排在前面的正则（`naming.Parse` 仍按表的优先级认，两者只共用正则），季绑定存它的副本；默认规则取自 `naming.EpisodePatterns`（`internal/naming`），即 `naming.Parse` 那张表里带 `episode` 组的写法，再加上认结尾 `/ N` 的一条：标签有两级标题时用 `source.LabelSeparator`（` / `）拼成"上级 / 下级"，越靠后越具体（见 `CollectionItem.Label`），新的源适配器拼标签也要照这个约定。
  - `NumberItems` 先按规范化的 ref 去重（`UniqueRefs`，保存合集的条目时也用它），按规则编号的合集再认出序号，最后标出重复序号（`markDuplicateNumbers`）；`NumberLabels` 只接收名称，认序号、标重复，不去重，结果与名称一一对应（文件夹的季绑定用它，名称为"季绑定标题 / 条目名称"）。
- B 站适配器的链接解析（`bilibili/link.go`）只做字符串分类（短链先跳转一次再分类），集面板与季面板各自决定接受哪些（集面板遇到合集的链接时提示到季面板）。
- 源适配器返回 `*source.Error`（带 Kind）：补建、定时拉取、标为失效都按 Kind 分支；管理 API 由 `binding.SourceAPIError`（季绑定的预览、创建、保存也用它）按 Kind 转成 400/422/502，只包装 Err，日志里提示不重复。

## 弹幕文件（ADR 0004）

- 上传的 B 站 XML 弹幕文件建绑定，不实现 `source.Adapter`。`danmakufile` 领域包只做解析（XML 的解码与字段映射在 `danmaku/bilifmt`，B 站适配器共用）；业务在 `internal/binding/service/file.go`（在集上上传的创建、追加文件、重新解析、文件列表）。
- 绑定的 `kind` 区分 `link` / `file`：文件绑定的 `adapter`、`ref`、`duration` 为空（CHECK 约束守住，唯一约束因此只作用于链接绑定）。
- 原文件存 `binding_files`（按 sha256 在绑定内去重），份数 `file_count` 与 `danmaku_count` 一样在事务里维护；弹幕不属于任何平台，原始 ID 直接用 dmid。
- `binding.Service` 的 `view`、`platform` 按 kind 分支，只对一种绑定有效的操作用在另一种上时返回 400。
- 上传的 handler 先调用 `request.ReadFiles`（`internal/infra/httpx/request`，单集上传和按季上传共用；文件可以省略的请求用 `request.ReadOptionalFiles`，上限经 `request.FileLimits` 传入），然后才 `request.Bind`：它给请求体套上 `http.MaxBytesReader`，用 `MultipartReader` 逐个 part 流式读取，边读边查份数、单份和合计大小，不落临时文件，也没有 `ParseMultipartForm` 的 1000 个 part 上限；文件以外的字段（合计最多 1 MB）读完后放回 `Request.Form`，所以之后的 `Bind` 照常可用。文件名取请求里原样的 filename，不像 `Part.FileName` 那样去掉目录（按季上传用它传相对路径）；单集上传自己再取 base name。

### 按季上传

按季上传创建、保存的是文件夹的季绑定，流程、事务和预览见 [`season-binding.md`](season-binding.md#文件夹)（ADR 0010）。这里只说弹幕文件的部分：

- 文件存在季绑定名下，不在绑定上：上传的文件夹由 `seasonbinding`（`folder.go`）分成条目、逐份用 `danmakufile.Parse` 解析，写进 `season_binding_entry_files`（文件名取 base name，条目内按 sha256 去重）。与单集上传共用的只有文件类型 `danmakufile.File`（按季上传时 `Name` 是相对路径）。
- 建出的文件绑定由 `binding.Service.CreateFileSlotInTx` 写入：带 `season_binding_id` 和 `entry_id`，标题是条目名称，弹幕是条目的各份文件合并后的；没有 `binding_files` 行，`file_count` 为 0。
- `binding` 在自己的 `queries.sql` 里读条目的文件（`ListEntryFiles`、`GetEntryFileContent`、`CountEntryFiles`）：文件列表、重新解析读条目的文件，绑定 JSON 的 `sourceLabel` 里的份数是条目的文件份数。文件列表里条目的文件没有加入时间，`uploadedAt` 为 null。
- 写入是同步的：带文件的请求前端不设超时（`uploadRequestTimeout`，单集上传、追加文件也是），不带文件的保存用 `slowRequestTimeout`；HTTP 服务也不限整个请求的读写时长，只限制读请求头和空闲连接（见 `server.go`）。

## 管理界面

- 创建绑定、重新拉取由后端当场拉取（最长约 25 秒），`bindings.ts` 里用 `slowRequestTimeout` 放宽超时，界面上用 `useElapsed` 显示已用秒数。
- 绑定卡片（`BindingCard`）按 `kind` 分出两组操作（`BindingSourceActions.tsx`：重新拉取 / 追加文件、重新解析），各自持有 mutation；一个绑定上改动弹幕的变更都带 `bindingKeys.write(id)` 前缀（`use-bindings.ts`），卡片用 `useIsMutating` 在任何一个进行中时禁用其他操作。
- 季绑定建出的绑定（`seasonBindingId` 不为空）和手动的排在同一个列表里，归季绑定管（后端的限制见 [`season-binding.md`](season-binding.md#建出的绑定)）：
  - 卡片右上角是带锁的标签，只写季绑定的名称，点它跳到季面板。集面板把剧详情里这一季的 `seasonBindings` 交给 `BindingCard`，由 `seasonBindingTag`（`season-binding.ts`）按 ID 查出季绑定，给出文字、悬停提示（全名和来源）和季面板的路径；名称取法与季绑定卡片一致（`seasonBindingName`：文件夹的季绑定为文件夹名，合集的季绑定为合集标题、为空时用合集的标签），过长时截断；查不到时只写"季绑定"。
  - 删除按钮的位置换成"在季绑定里改 →"，同样跳到季面板，悬停时说明为什么不能单独删除。
  - 偏移照常能改；链接绑定照常重新拉取、清空后重新拉取；文件绑定只能重新解析，没有追加文件（`FileBindingActions`），弹出层里的文件列表是条目的文件、只读，没有加入时间（`BindingFilesPopover`）。
- 弹幕文件列表的键 `['binding-files', id]`，弹出层打开时才取。
- 查看绑定的弹幕（`BindingDanmakuDialog`，点卡片上的弹幕条数打开）：`GET /api/bindings/:id/danmaku?fromMs=&after=` 按 `(time_ms, source_id)` 游标分页，每页 200 条，时间未校正，不输出原始 ID（可能超出 JS 的安全整数）；`fromMs` 用于跳转，翻页时照传。绑定 JSON 带着 `contentVersion`（插入了新弹幕或替换全部弹幕时加 1）和 `maxTimeMs`（最晚一条弹幕的时间，拖动条的长度），两者与 `danmaku_count` 一样在写入弹幕的事务里维护。前端的无限查询键是 `['binding-danmaku', id, contentVersion, fromMs]`：不论弹幕怎么变的，剧详情重新加载后版本一变就换一份数据，同一个键不会过时，不重新请求。

## B 站适配器的测试

- 平时只回放 `internal/source/bilibili/testdata/` 里脱敏后的样本。测试里假 B 站换掉的是适配器 `http.Client` 的 Transport：各个域名（API、XML 弹幕、短链）的请求都转给它，按 Host 区分接口。
- live 模式请求真实的 B 站，默认关闭，CI 不请求：`go test ./internal/source/bilibili -run TestLive -args -live` 只验证；再加 `-update` 时先清空 `testdata/`，把响应脱敏后写进去（原始响应不落盘，限流、接口异常这类出错的响应不录制）。
- 在改动 B 站适配器之后和里程碑验收时各跑一遍，全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速；以未登录的身份请求，港澳台限定番剧的用例要求从大陆的网络请求。
- 固定的公开视频列表（集面板的单集、季面板的合集）、脱敏规则见 `live_test.go` 开头，列表只挑内容中性的视频和番剧，状态变了（包括列表里连载中的番剧完结了）就换一个再重新录制。

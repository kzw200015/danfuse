# 季绑定与补建

季绑定分两种，由 `season_bindings.kind` 区分：合集的季绑定（`collection`）在季上绑定一个合集，在后台补建出链接绑定，可以追更；文件夹的季绑定（`folder`）保存上传的弹幕文件夹，当场建出文件绑定。两种都保存一份**表单**，各集的绑定一律由表单派生，放在季绑定在那一集的**槽位**里（ADR 0010）。都归 `seasonbinding.Service`，槽位里的绑定由 `binding.Service` 写入。术语（表单、条目、槽位、补建、替换）见 `GLOSSARY.md`，合集与集号规则、弹幕文件见 [`sources.md`](sources.md)。

## 文件

下表是 `service/` 里的文件；`handler/` 里是入口 `routes.go`（`Handler`、`Register`）和同名的 `season_binding.go`、`collection.go`、`folder.go`，默认规则与模板生成在 `episode_rule.go`。

| 文件 | 内容 |
| --- | --- |
| `derive.go` | 派生函数 `Derive` |
| `derive_input.go` | 派生的输入从哪里来：`storedInput`（详情）、`collectionEntries`、`storedCollection`、`folderEntries`、`episodeSlots` |
| `collection.go` | 合集的预览、创建、保存：`listCandidates`、`resolve`、`chooseCandidate`、`saveItems`、`saveListed` |
| `backfill.go` | 合集的后台循环、租约、追更的扫描、一轮补建 |
| `folder.go` | 文件夹的分组、解析、对上条目、写条目，预览和四种写入（`applyFolder`） |
| `service.go` | `Service`、构造、错误与共用的取、锁 |
| `season_binding.go` | 不分种类的操作：详情 `Get`、剧详情里的列表 `ListBySeries`、开关追更 `SetFollow`、按种类分派的立即补建 `Backfill`、删除 `Delete` |
| `view.go` | JSON：`View`（公共字段加 `*CollectionView` / `*FolderView`）、`Detail`、`ItemView`、`SlotView`、`Preview`、`Candidate`、`Summary` |

## 数据

- `season_bindings` 两种共用：
  - 两种都有：标题、版本化集号规则 `episode_rule`（JSONB）、集号对应 `mapping_from`/`mapping_to`。配置中的实际正则、顺序、加减值、排除项随绑定保存，不按未来默认表重新解释。
  - 只属于合集：`adapter`、`ref`、`link`、`follow`、`numbered_by_rule`、`status`、`last_error`、`last_checked_at`。由 CHECK `season_bindings_kind_columns_check` 按 kind 守住：文件夹的这些列为空、追更关着、状态为正常。`follow` 列的默认值是开着，插入文件夹的季绑定要写明 `follow = false`（`InsertFolderSeasonBinding`）。
  - 唯一约束 `(season_id, adapter, ref)` 在 NULL 上不生效，同一个文件夹可以在一季上传多次。
  - `link` 是创建或保存时贴的链接，原样保存，只用来在编辑时预填；合集以 ref 为准，链接不进 ref（理由同 ADR 0009 第 1 条）。
- `season_binding_items` 是合集上次列出的结果，不是表单。主键 `(season_binding_id, ref)`。`saveItems` 每次列出成功后按规范化的 ref 去重、增改，删掉不在了的。`label` 保留展示标题，`label_parts` 可空 text[] 保存真实标题层级。`number` 只存平台给的集号，按规则编号的合集为空，读取时再按季绑定现在的规则编号，避免重复应用加减。`last_error`、`last_error_at` 是条目上次补建失败的原因，补建成功、或撞上同源约束跳过后清掉。条目的状态、诊断读取时现算，不存储。
- 文件夹的表单是条目 `season_binding_entries` 与条目的文件 `season_binding_entry_files`：
  - 条目名称 `label` 不带文件夹名（子目录名，或去掉扩展名的文件名），唯一约束 `(season_binding_id, label)`。
  - 文件按 `(entry_id, sha256)` 去重，没有加入时间。
- `bindings` 上与季绑定有关的列和约束：
  - `season_binding_id` 外键级联删除。
  - 文件绑定的 `entry_id` 与它组成复合外键，指向 `season_binding_entries (id, season_binding_id)`，同样级联删除。
  - CHECK `bindings_entry_id_check`：链接绑定的 `entry_id` 为空；文件绑定的这两列要么都有，要么都没有。所以在集上手动上传的文件绑定不带季绑定。
  - 槽位是唯一约束 `(season_binding_id, episode_id)`。
  - 同源是唯一约束 `(episode_id, adapter, ref)`，只作用于链接绑定。插入链接绑定写明 `ON CONFLICT (episode_id, adapter, ref) DO NOTHING`，撞上它返回 `ErrSourceBound`；撞上槽位约束是程序错误。
- 建出的文件绑定没有 `binding_files` 行，`file_count` 为 0，标题是条目名称；文件列表、重新解析、卡片上的份数都读条目的文件（`binding` 的 `storedFiles`、`view`）。
- 约束表达不了的跨表规则（链接绑定只由合集的季绑定建出等）由代码保证，`testenv.AssertInvariants` 检查（见 [`testing.md`](testing.md)）。

迁移 `00011` 按原顺序把 `episode_patterns` 转为 version 1（完整范围、加减零、无排除），已有 `label_parts` 保持 NULL。不改已有绑定、弹幕、时间偏移、追更、映射或平台集号。后台补建可以刷新标题层级，但不切换规则版本。用户在编辑预览中主动切换新版时重新列出合集，保存后详情与补建使用这份配置；新版遇到历史缺失层级缓存时提示"标题信息不完整"。Down 只有全库均为 version 1 时可还原，存在 version 2 即拒绝有损回退。

## 派生

`Derive(entries, mapping, episodes)` 是纯函数，只管集号对应和槽位。编号、排除、集号加减、调整后重复、按 ref 去重都由 `source.NumberItems` / `NumberEntries` 事先做好，所以对到同一集的条目至多一个。完整的规则流水线见 [`sources.md`](sources.md#集号规则adr-0011)。

- 输入：
  - `Entry.Source` 是弹幕源的身份：合集为 `source.CanonicalRef(ref)`，文件夹为条目 ID。
  - `Episode.Slot` 是这个季绑定在那一集建出的绑定。
  - `Episode.Occupied` 是那一集上别的绑定占用的、这个适配器的弹幕源；只对合集，文件夹为 nil。
- 每个条目依次判断：对不上（`unmatched`，原因原样输出）→ 序号在起点之前（`beforeStart`）→ int64 计算并检查本地集号范围 → 目录里没有对到的集（`noEpisode`）。对到了集时再看：
  - `Occupied` 里有它的弹幕源：已有同源绑定（`alreadyBound`），槽位不动，也不算空；
  - 槽位为空：新建（`create`）；
  - 槽位里是同一个弹幕源：保留（`keep`）；
  - 槽位里是别的弹幕源：替换（`replace`），`Item.Replaces` 是原来的槽位。
- `Result.Empty` 是没有条目对到的槽位，按集号排列：保存表单时删除，补建时留着。
- 汇总（新建、替换、删除、保留各几个）由 `newCandidate` 从 `Items` 和 `Empty` 数出。
- 进入派生的 ref 都经过 `source.CanonicalRef`（键排序、紧凑、数字原样）：适配器列出的条目、`season_binding_items.ref`、`bindings.ref`。否则从 jsonb 读出的文本带空格、键会重排，所有槽位都会被判成替换。规范化的 ref 是合法的 JSON，补建时原样交给适配器的 `Fetch`。
- 输入从哪里来（`derive_input.go`）：
  - 合集的预览、保存、补建用这次列出的合集 `source.NumberItems(col, 规则)`；详情用 `storedCollection` 把条目表拼回 `source.Collection` 再编号，番剧条目的 `number` 为空时原因写"平台没有给出整数集号"。
  - 文件夹的条目用 `source.NumberEntries` 认集号（`folderEntries`），明确传入 `LabelParts: [季绑定标题, 条目名称]`，`label` 仍按 `source.LabelSeparator` 展示为"季绑定标题 / 条目名称"。库里只存条目名称；新建、换文件夹时标题取这次的文件夹名。
  - `episodeSlots` 查一季的集和槽位（`ListSeasonEpisodeSlots`），合集另查 `ListSeasonOccupiedSources`。预览和详情在读取时查，保存和补建在写入事务里查。

## 写入

三种写入场景：

| 场景 | 新建 | 替换 | 删除没有条目的槽位 |
| --- | --- | --- | --- |
| 保存表单（包括创建） | ✓ | ✓ | ✓ |
| 补建（合集在后台；文件夹当场完成，即立即补建） | ✓ | ✓ | ✗ |

- 合集保存时，事务里只做删除，新建和替换交给紧接着的后台补建。
- 替换是删掉旧的绑定、新建一个，同一个事务里先 `DeleteSlotsInTx` 再 `CreateLinkSlotInTx` / `CreateFileSlotInTx`，不挪动原来的绑定，新绑定的偏移归零。
- 加锁：写入事务的第一句锁季（`FOR KEY SHARE`），再锁季绑定，写一集的绑定之前先锁那一集（`FOR KEY SHARE`），即季 → 季绑定 → 集 → 绑定。不死锁靠先锁季：
  - 删季（删剧）的第一步是锁住季那一行，与 `FOR KEY SHARE` 冲突，两边在季上排队，所以之后的顺序不必与删季的级联（季 → 集 → 季绑定 → 绑定）一致；
  - 只删一集时只锁这一集和它的绑定，写入同样先锁集、再写这一集的绑定；
  - 同一个季绑定的写入由 `FOR UPDATE` 锁季绑定（保存表单、删除、文件夹的写入，`lockSeasonBinding`）或补建租约（合集的补建）串行；不同季绑定的写入对集只加 `FOR KEY SHARE`，互不阻塞。所以锁多集时不必按固定顺序（文件夹建出槽位时逐集锁）。
- `binding.Service` 给季绑定的方法在 `binding/service/season_slot.go`。调用方开事务，先锁好季和季绑定；这些方法先锁集，再写绑定：
  - `CreateLinkSlotInTx(ctx, tx, LinkSlot)`：集已被删除时返回 `ErrEpisodeGone`，集上已有同一个弹幕源时返回 `ErrSourceBound`、什么都不写。提交之后调用方调用 `LogFetched`。
  - `CreateFileSlotInTx(ctx, tx, FileSlot)`：弹幕是条目的各份文件经 `danmakufile.Parse` 解析、合并后的，按原始 ID 去重。集上已有别的绑定时照常建（文件没有"同源"）。
  - `DeleteSlotsInTx(ctx, tx, 季绑定 ID, 集 ID)`：先按 ID 顺序锁住这些集（`LockEpisodes`），再删绑定，弹幕随之级联删除。

### 合集

- **创建**（`CreateCollection`）：
  1. 在事务之外由 `resolve` 识别链接、按 `collectionKind` 选候选（`chooseCandidate`）、查重（`SeasonBindingExists`）、列出合集，识别与列出共用 `source.FetchTimeout`。
  2. 事务里锁季，插入季绑定（同时绑定同一个合集时撞上唯一约束，返回 409），保存这次列出的结果。
  3. 触发后台补建，返回 201 和详情。新建的季绑定开着追更。
- **保存表单**（`SaveCollection`）：
  1. 拿这个季绑定的补建租约（`tryBackfillLease`），拿不到返回 409"正在补建或保存，稍后再试"。持有到写入提交，期间用租约的 ctx。
  2. 由 `resolve` 重新识别链接、选候选、查重（不算自己）、列出合集。删除以这次列出的结果为准，不和预览核对。
  3. 事务（`saveCollectionTx`）：
     - 锁季，`FOR UPDATE` 锁季绑定（`lockSeasonBinding`）；
     - 再检查规则版本，拒绝把已存 version 2 的绑定写回旧格式/version 1（409，要求刷新客户端）；`SaveCollectionForm` 写表单，换成另一个合集（适配器或 ref 变了）时清掉上次检查时间；同一季已有这个合集时撞上唯一约束，返回 409；
     - `saveListed` 保存这次列出的结果（季绑定恢复为正常、更新标题与是否按规则编号、写条目表）；
     - 派生，用 `DeleteSlotsInTx` 删掉 `Empty`。
  4. 释放租约，记一条 `season binding saved`（带删掉的槽位数），触发后台补建，返回详情。

  每次保存都走这一套，不比较表单改没改：没改也能保存，用来清掉补建留下的空槽位。
- **预览**：
  - `PreviewNewCollection` 与 `PreviewCollectionEdit` 都由 `listCandidates` 列出链接的每个候选，各自派生成一组（`collectionPreview`）。
  - 不看 `collectionKind`，不写库，不缓存，每次都请求平台。
  - 新建的预览没有槽位；编辑的预览按季绑定现在的槽位派生，`current` 标出 ref 与保存的合集相同的候选。
- **开关追更**（`SetFollow`，`PATCH {follow}`）：不拿租约，不删绑定，打开时触发一次补建。
- 适配器的错误经 `binding.SourceAPIError` 转成 400/422/502，选不出候选为 400。

### 后台补建与追更

- `Run` 由 `App.Run` 和同步一起运行（循环见 [`runtime.md`](runtime.md)）：
  - 手动触发（创建、保存、立即补建、打开追更）经它的循环（`trigger`）拿到租约，立即在后台开始；不同季绑定的补建互不等待。
  - 立即补建正在补建时返回 409"正在补建"，服务正在关闭时返回 503；创建、保存、打开追更后的触发（`startBackfill`）遇到这两种只记日志。
- 补建是异步的（立即补建返回 202 和详情），而创建绑定、重新拉取一个绑定是同步的：一轮补建的条目数不定、会遇到限流，放进请求里会超时，也需要租约防止同一个季绑定同时跑两轮。
- "补建中"以按季绑定的租约为准（`leases` 表里有没有没过期的 `season_backfill:<ID>`，`database.LeaseSeasonBackfill`），季绑定上不存运行状态。保存表单也持有这个租约，所以保存与补建互斥。
- 一轮补建（`backfill` → `runBackfill`）：
  1. 在事务之外列出合集。失败时不动绑定和条目表：合集不存在标为失效，限流和其他上游错误记下原因，都结束这一轮。
  2. 一个事务里 `saveListed` 保存列出的结果，同时读出表单（锁住季绑定的那一句读，保存表单的事务要么已经提交，要么等这个事务提交），然后派生。
  3. 逐个处理"新建"和"替换"的条目（`backfillItem`）：
     - 在事务之外拉取；
     - `saveSlot` 的事务：锁季，`LockSeasonBindingShared`（`FOR KEY SHARE`）锁季绑定（季绑定没了就结束这一轮）；替换时先 `DeleteSlotsInTx`，再 `CreateLinkSlotInTx`；清掉条目的失败原因。

     失败的处理：
     - 集被删了（`ErrEpisodeGone`）：跳过这个条目；
     - 撞上同源约束（`ErrSourceBound`）：事务回滚，旧的绑定保留，计入已有同源绑定，另外清掉条目上次失败的原因；
     - 限流：记下原因，结束这一轮；
     - 其他拉取失败：原因记在条目的 `last_error`、`last_error_at` 上（`setItemError`），继续下一个。
  4. 正常结束或因错误、限流结束时，`FinishSeasonBindingCheck` 把上次检查时间写为这一轮的开始时间：补建期间同步进来的集仍算上次检查之后才有的。被关闭服务或丢失租约打断时不写，下一次扫描接着做。结束时记一条 `backfill finished`，记下了错误时为 warn 级别。现有日志增加 `rule_version`、`recognized`、`excluded`、`duplicates` 计数，文件夹补建日志同样记录，不引入新任务或服务。
- 补建只建出绑定，不重新拉取已经建出的绑定：之后的重新拉取是定时拉取（[`scheduled-fetch.md`](scheduled-fetch.md)），与追更开关、季绑定的租约都无关。
- 追更的扫描（`scan`）由同一个循环每隔 `follow.scan_interval`（默认 1 分钟）在后台开始一次，一次补建一个，不等上一次扫描做完（同一个季绑定靠它的租约不会重复补建）。
  - 到期的判定见 `ListDueSeasonBindings`：从没检查过、满一个检查周期 `follow.check_interval`（默认 12 小时）、季里有晚于上次检查建出的集。
  - 拿到租约之后用同一条查询按 ID 再确认一次（`scanOne`），列出之后才被检查过的跳过。
  - 文件夹的季绑定追更恒为关，扫描不会碰到它。
- 追更时间规则是配置项（`config.Follow`，经 `seasonbinding.NewService` 传入，设置接口也返回它，前端的追更说明由 `lib/follow.ts` 按它写出）。与追更比较的时间（上次检查）和建出的绑定的建出时间都取自应用的时钟（`time.Now()`），不用数据库的 `now()`，测试才能用假时间推进；集的建出时间仍是数据库写入的。

### 文件夹

- 四种写入当场完成，不拿租约，靠 `FOR UPDATE` 锁季绑定（`lockSeasonBinding`）与另一次写入排队：
  - 创建 `CreateFolder`；
  - 保存表单 `SaveFolder`，不带文件时只改集号规则、集号对应，带文件时换文件夹；
  - 立即补建 `backfillFolder`，由 `Service.Backfill` 按种类分派，返回 200 和详情。
- 有上传的文件时，先在事务之外由 `readFolder` 分成条目（`groupFolder`）、逐份解析（`binding.ParseFiles`，与在集上上传弹幕文件同一套）。结构不对为 400，有一份解析失败为 422（提示带着它的相对路径），什么都不保存。分组规则与前端的 `groupFolderFiles` 相同：
  - 路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"，最多两层，文件夹名都相同；
  - 只认 `.xml`，不分大小写；
  - 子目录合成一个条目，顶层的文件各自一个条目；
  - 条目名称不能重复。
- 事务：
  1. 锁季、锁季绑定并检查 version 2 不得降级；创建时插入季绑定（标题为文件夹名），保存时 `UpdateFolderSeasonBinding` 写规则和对应，换文件夹时标题改为新的文件夹名。
  2. `applyFolder` 落实表单：
     1. 定出条目：换文件夹（和创建）时为这次上传的，由 `matchEntries` 按"名称 + 文件哈希的集合"对上保存的条目，对上的沿用条目 ID，没对上的用临时身份 `new:<名称>`，它不会与任何槽位相同；文件夹名不参与，改名后照样对得上。不换文件夹时为保存的条目。
     2. 派生。
     3. 先用 `DeleteSlotsInTx` 删掉要替换的槽位，以及（立即补建以外）没有条目的槽位。没对上的条目建出的绑定都在这两类里。
     4. 换文件夹时由 `writeEntries` 写条目：删掉没对上的，沿用的改位置，新的连同文件插入。
     5. 新建、替换：从条目的文件解析，用 `CreateFileSlotInTx` 写入；集已被删除时跳过。
  3. 记一条 info 日志（建出、替换、删除的槽位数），返回详情：创建是 201，其余是 200。
- 对不上的条目也保存，以后改了集号规则或集号对应可能就对上了。
- 预览不上传文件，请求带文件夹名和各条目的名称与文件哈希 `{folder, entries:[{label, sha256s}]}`：
  - `PreviewNewFolder` 的条目都是新的；
  - `PreviewFolderEdit` 不换文件夹时省略这两项，按保存的条目和标题算，换文件夹时同样经 `matchEntries` 对上保存的条目。
  - 只有一组，没有合集的字段（`folderPreview`）。
- 份数与合计大小的上限用 `danmaku_file.season_max_files` / `season_max_upload_mb`，单份仍是 `max_file_mb`（`Handler.folderLimits`）。multipart 由 `request.ReadFiles`（保存时 `ReadOptionalFiles`）流式读取，见 [`sources.md`](sources.md#弹幕文件adr-0004)。

### 删除

`Delete` 两种一样，不拿租约：一个事务里先锁季、再 `FOR UPDATE` 锁住季绑定（`lockSeasonBinding`），再删掉它；建出的绑定（连同弹幕）、条目表、条目和条目的文件随外键级联删除。进行中的补建等这个事务提交后再也锁不到季绑定，随即结束这一轮。

## 管理 API

```
GET    /api/episode-rules/default
POST   /api/episode-rules/template                        {templateId, season?, adjustment} → {rule, example}
POST   /api/seasons/:id/season-bindings/collection/preview  {link, episodeRule, mappingFrom, mappingTo} → 预览
POST   /api/seasons/:id/season-bindings/collection          {link, collectionKind, episodeRule, mappingFrom, mappingTo} → 201 详情
POST   /api/seasons/:id/season-bindings/folder/preview      {folder, entries, episodeRule, mappingFrom, mappingTo} → 预览
POST   /api/seasons/:id/season-bindings/folder              multipart：files + form → 201 详情
GET    /api/season-bindings/:id                             详情
POST   /api/season-bindings/:id/collection/preview          同新建 → 预览
PUT    /api/season-bindings/:id/collection                  同新建 → 详情
POST   /api/season-bindings/:id/folder/preview              {folder?, entries?, ...} → 预览
PUT    /api/season-bindings/:id/folder                      multipart：files（可省）+ form → 详情
PATCH  /api/season-bindings/:id                             {follow} → 详情
POST   /api/season-bindings/:id/backfill                    合集 202、文件夹 200，都带详情
DELETE /api/season-bindings/:id
```

- multipart 里 `files` 的文件名是以文件夹名开头的相对路径，`form` 是 JSON `{episodeRule, mappingFrom, mappingTo}`（`parseFolderForm`）。集号规则和集号对应由各请求嵌入的 `ruleForm` 在 `Validate` 里解析。
- 兼容请求可用 `episodePatterns: string[]` 代替 `episodeRule`，解释为 version 1；同时提供两个字段（包括显式 null）为 400。version 2 绑定拒绝旧字段或 version 1 的写回（409），检查既在前置读取也在锁住季绑定的事务中，防止并发覆盖。
- 默认接口返回旧 `episodePatterns`、新版 `episodeRule` 和模板元数据 `templates`；模板接口不请求平台、不访问数据库，由 `source.RenderEpisodeTemplate` 生成实际正则和示例。除季号类与 `sxe` 外传 `season` 会报参数错误。
- `View` 继续输出 `episodePatterns` 的正则投影，同时带完整 `episodeRule`；旧投影无法表达加减和排除，客户端应使用完整结构。
- 种类不符时返回 400，例如对合集的季绑定调用 `/folder`（`getKind`）。
- 剧详情里的 `seasonBindings[]` 是 `View`：两种都有的字段，加上合集的（`adapter`、`link`、`sourceUrl`、`sourceLabel`、`numberedByRule`、`follow`、`status`、`lastError`、`lastCheckedAt`、`running`）或文件夹的（`entryCount`）。
- 详情是 `View` 加 `items`（按补建的语义派生，不删除）和 `emptySlots`；合集的条目带 `lastError`、`lastErrorAt`，文件夹的恒为 null。详情不请求平台。
- 预览是 `{candidates}`，每组带 `items`、`removed`（保存后删除的槽位）、`summary`。
- `ItemView` 保留既有 status 和 number 语义，另带 `reasonCode` 与可选 `trace`。原因码区分 `noMatch`、`invalidNumber`、`nonRegular`、`customExcluded`、`adjustmentOutOfRange`、`mappingOutOfRange`、`duplicateNumber`、`missingLabelParts`。重复号码被清空后仍通过 trace 显示号码及冲突行和标题；诊断不持久化、不输出原始 ref。

## 建出的绑定

- `bindings.season_binding_id` 非空的绑定归季绑定管：`binding.Service` 的 `Delete`、`AppendFiles` 对它返回 409（`checkNotBuilt`，提示写明季绑定的名称）；偏移照常能改，链接绑定能重新拉取，文件绑定能重新解析。
- 管理界面上的限制见 [`sources.md`](sources.md#管理界面)。

## 管理界面

- 季面板（`SeasonPanel`）右上角"绑定合集""上传弹幕文件夹"在卡片列表上方展开添加的表单，同一时间只展开一个，展开状态记着季 ID，换一季时自然收起。
- 两种表单各一个文件：`CollectionForm.tsx`（`AddCollectionForm`、`EditCollectionForm`）、`FolderForm.tsx`（`AddFolderForm`、`EditFolderForm`）。
  - 添加与编辑的不同之处（文案、初始值、`preview`、`save`、`onClose`）各自写成一个模式对象（`CollectionFormMode`、`FolderFormMode`），表单只经由它区分两种模式。
  - 编辑在卡片里内联展开，预填保存的表单，打开就预览。合集的编辑默认选中 `current` 的候选。
  - 文件夹选好后由 `groupFolderFiles` 分组（条目名称不带文件夹名，文件夹名单独返回），`hashEntries` 算出各份文件的 SHA-256 发给预览。没有 `crypto.subtle`（用 http://局域网 IP 打开）时用 `@noble/hashes` 兜底。
- 共用的部分在 `SeasonBindingFormParts.tsx`，只接收预览结果和保存：
  - `RuleFields` 是集号规则与集号对应的输入框，`useRuleDraft` 管它们的状态；
  - `PreviewSection` 是汇总标签（`summaryTags`）、`PreviewTable`（要删的槽位接在表尾）和带确认的保存。
- 新建用默认 version 2、下级范围、仅正片开启、映射 `1 = 1`。`EpisodeRuleInput` 以模板为入口，选择格式、适用时的来源季、加减值；`useEpisodeTemplate` 查询后端生成正则。"仅使用此规则"替换正向规则，"添加覆盖规则"插到首位。每条显示加减，高级编辑提供正则、排序、删除和自定义排除。
- 匹配范围属于整组；仅正片开关加入默认 `nonRegular` 排除快照，关闭时只移除这一类，保留 custom。加减的命名与弹幕时间偏移分开。
- 已有绑定按其保存版本打开，version 1 显示"旧版规则"并原样预览。"使用新版模板"替换草稿规则、保留映射，不自动迁移自定义正则；取消后恢复原配置。升级和普通修改一样等待预览、确认删除/替换、保存，沿用租约、事务和槽位。
- `useSeasonBindingPreview`（`use-season-bindings.ts`）让预览随表单自动刷新：
  - 查询键是防抖（`previewDebounce`，600ms）后的输入，不缓存，重新预览时保留上一次的结果。
  - `current` 为 false（还没防抖完、正在预览、预览失败）时旧预览标为过期，不能保存；保存不比较表单改没改。
  - 有删除或替换时，保存前用确认框逐条列出要删的绑定，并说明替换的集偏移归零。
- 季绑定卡片（`SeasonBindingCard`）两种共用：
  - 卡片上只读地显示集号对应，集号规则和集号对应都在"编辑"里改。
  - 合集另有追更开关、状态、上次检查和"补建中"。
  - 删除的确认框写明建出的绑定会连同弹幕一起删除，文件夹另加"上传的文件也一起删除"。
  - `EntryTable` 用 `itemStatusText` 写出各条目的状态，上次失败的标红；表下用黄色提示列出 `emptySlots`。
- 预览与详情共用 `ItemTable` 的可展开诊断。排除项显示"已排除"，重复项显示冲突号码，诊断中的按钮按本表行号滚动并聚焦其他冲突行。
- 季绑定的详情（`useSeasonBinding`，键 `['season-bindings', id]`）只在展开条目表、或合集正在补建时取。正在补建时每秒轮询，结束后让剧详情失效；季绑定的列表在剧详情里。
- 写入成功后用 `useWatchSeasonBinding` 把返回的详情放进缓存，合集的轮询随即开始；两种季绑定都另外重新加载剧详情（文件夹的写入当场建出绑定，合集保存时当场删除没有条目的槽位）。

## 测试

- `derive_test.go` 表驱动，不连数据库，覆盖派生的每一种判断。
- 其余季绑定的测试都在 synctest 气泡里（`helpers_test.go` 的 `newSeasonEnv`）：
  - 第 1 季的集按假时间写入建出时间，`seasonbinding.Service.Run` 在后台运行。
  - `synctest.Wait` 等后台的补建停下，`time.Sleep` 推进追更的扫描和 12 小时的边界（追更的时间规则用 `testFollow`，与配置的默认值一致）。
  - 读结果用 `states`（条目表）、`emptySlots`、`bindings`（库里的全部绑定）。
  - 文件夹的槽位可以由 `env.folder` 直接写入（`testenv.InsertFolderSeasonBinding` 加 `CreateFileSlotInTx`，不经过派生）。
  - 要让写入事务等锁时用 `holdRowLock` 在另一个事务里锁住行，`waitLockWaiters` 等到有连接在等锁。
- `testenv.FakeCollector` 每次拉取在 `Started` 上报弹幕源名字、从 `Release` 收到一次放行；气泡与并发用例的通用写法见 [`testing.md`](testing.md)。
- 路由、参数校验与状态码在 `server/season_binding_test.go`。
- `source/episode_config_test.go` 覆盖多季合集的标题、模板限定季、优先级、归一化、边界、加减与排除；旧版 `source/episode_rule_test.go` 继续验证原识别结果并验证新诊断。`service/episode_rules_test.go` 用真实 PostgreSQL 验证合集 12→25 集的预览/保存/补建一致性、保留 ID/弹幕/偏移、缓存标题和文件夹对等行为；`infra/database/migration_00011_test.go` 验证迁移往返及有新版配置时拒绝回退。
- 前端 `EpisodeRules.spec.tsx` 和 `SeasonBindings.spec.tsx` 验证模板替换/前置、仅正片与 custom 共存、主动升级与取消、冲突定位、过期预览禁存及原有删除/替换确认。

# 季绑定与补建

季绑定分两种，由 `season_bindings.kind` 区分：合集的季绑定（`collection`）在季上绑定一个合集，在后台补建出链接绑定，可以追更；文件夹的季绑定（`folder`）保存上传的弹幕文件夹，当场建出文件绑定。两种都保存一份**表单**，各集的绑定一律由表单派生，放在季绑定在那一集的**槽位**里（ADR 0010）。都归 `seasonbinding.Service`，槽位里的绑定由 `binding.Service` 写入。术语（表单、条目、槽位、补建、替换）见 `GLOSSARY.md`，合集与集号规则、弹幕文件见 [`sources.md`](sources.md)。

## 文件

下表是 `service/` 里的文件；`handler/` 里是入口 `routes.go`（`Handler`、`Register`）和同名的 `season_binding.go`、`collection.go`、`folder.go`。

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
  - 两种都有：标题、集号规则 `episode_patterns`、集号对应 `mapping_from`/`mapping_to`。
  - 只属于合集：`adapter`、`ref`、`link`、`follow`、`numbered_by_rule`、`status`、`last_error`、`last_checked_at`。由 CHECK `season_bindings_kind_columns_check` 按 kind 守住：文件夹的这些列为空、追更关着、状态为正常。`follow` 列的默认值是开着，插入文件夹的季绑定要写明 `follow = false`（`InsertFolderSeasonBinding`）。
  - 没有按季、合集的唯一约束：同一个合集可以在一季绑定多次（ADR 0011），同一个文件夹也可以在一季上传多次。`season_id` 上是普通索引。
  - `link` 是创建或保存时贴的链接，原样保存，只用来在编辑时预填；合集以 ref 为准，链接不进 ref（理由同 ADR 0009 第 1 条）。
- `season_binding_items` 是合集上次列出的结果，不是表单。主键 `(season_binding_id, ref)`。`saveItems` 每次列出成功后按规范化的 ref 去重、增改，删掉不在了的。`number` 只存平台给的集号，按规则编号的合集为空，读取时再按季绑定现在的集号规则编号。`last_error`、`last_error_at` 是条目上次补建失败的原因，补建成功、或撞上同源约束跳过后清掉。条目的状态读取时现算，不存储。
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

## 派生

`Derive(entries, mapping, episodes)` 是纯函数，只管集号对应和槽位。编号、认不出集号、集号重复、按 ref 去重都由 `source.NumberItems` / `NumberLabels` 事先做好，所以对到同一集的条目至多一个。

- 输入：
  - `Entry.Source` 是弹幕源的身份：合集为 `source.CanonicalRef(ref)`，文件夹为条目 ID。
  - `Episode.Slot` 是这个季绑定在那一集建出的绑定。
  - `Episode.Occupied` 是那一集上别的绑定占用的、这个适配器的弹幕源；只对合集，文件夹为 nil。
- 每个条目依次判断：对不上（`unmatched`，原因原样输出）→ 序号在起点之前（`beforeStart`）→ 目录里没有对到的集（`noEpisode`）。对到了集时再看：
  - `Occupied` 里有它的弹幕源：已有同源绑定（`alreadyBound`），槽位不动，也不算空；
  - 槽位为空：新建（`create`）；
  - 槽位里是同一个弹幕源：保留（`keep`）；
  - 槽位里是别的弹幕源：替换（`replace`），`Item.Replaces` 是原来的槽位。
- `Result.Empty` 是没有条目对到的槽位，按集号排列：保存表单时删除，补建时留着。
- 汇总（新建、替换、删除、保留各几个）由 `newCandidate` 从 `Items` 和 `Empty` 数出。
- 进入派生的 ref 都经过 `source.CanonicalRef`（键排序、紧凑、数字原样）：适配器列出的条目、`season_binding_items.ref`、`bindings.ref`。否则从 jsonb 读出的文本带空格、键会重排，所有槽位都会被判成替换。规范化的 ref 是合法的 JSON，补建时原样交给适配器的 `Fetch`。
- 输入从哪里来（`derive_input.go`）：
  - 合集的预览、保存、补建用这次列出的合集 `source.NumberItems(col, 规则)`；详情用 `storedCollection` 把条目表拼回 `source.Collection` 再编号，番剧条目的 `number` 为空时原因写"平台没有给出整数集号"。
  - 文件夹的条目按"季绑定标题 / 条目名称"（`source.LabelSeparator`，和多 P 投稿的"稿件标题 / 分 P 标题"一致）用 `source.NumberLabels` 认集号（`folderEntries`）。API 里文件夹条目的 `label` 也是拼好的名称；库里只存条目名称。新建、换文件夹时标题取这次的文件夹名。
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
  1. 在事务之外由 `resolve` 识别链接、按 `collectionKind` 选候选（`chooseCandidate`）、列出合集，识别与列出共用 `source.FetchTimeout`。
  2. 事务里锁季，插入季绑定，保存这次列出的结果。
  3. 触发后台补建，返回 201 和详情。新建的季绑定开着追更。
- **保存表单**（`SaveCollection`）：
  1. 拿这个季绑定的补建租约（`tryBackfillLease`），拿不到返回 409"正在补建或保存，稍后再试"。持有到写入提交，期间用租约的 ctx。
  2. 由 `resolve` 重新识别链接、选候选、列出合集。删除以这次列出的结果为准，不和预览核对。
  3. 事务（`saveCollectionTx`）：
     - 锁季，`FOR UPDATE` 锁季绑定（`lockSeasonBinding`）；
     - `SaveCollectionForm` 写表单，换成另一个合集（适配器或 ref 变了）时清掉上次检查时间；
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
  4. 正常结束或因错误、限流结束时，`FinishSeasonBindingCheck` 把上次检查时间写为这一轮的开始时间：补建期间同步进来的集仍算上次检查之后才有的。被关闭服务或丢失租约打断时不写，下一次扫描接着做。结束时记一条 `backfill finished`，记下了错误时为 warn 级别。
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
  1. 锁季、锁季绑定；创建时插入季绑定（标题为文件夹名），保存时 `UpdateFolderSeasonBinding` 写规则和对应，换文件夹时标题改为新的文件夹名。
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
POST   /api/seasons/:id/season-bindings/collection/preview  {link, episodePatterns, mappingFrom, mappingTo} → 预览
POST   /api/seasons/:id/season-bindings/collection          {link, collectionKind, episodePatterns, mappingFrom, mappingTo} → 201 详情
POST   /api/seasons/:id/season-bindings/folder/preview      {folder, entries, episodePatterns, mappingFrom, mappingTo} → 预览
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

- multipart 里 `files` 的文件名是以文件夹名开头的相对路径，`form` 是 JSON `{episodePatterns, mappingFrom, mappingTo}`（`parseFolderForm`）。集号规则和集号对应由各请求嵌入的 `ruleForm` 在 `Validate` 里解析。
- 种类不符时返回 400，例如对合集的季绑定调用 `/folder`（`getKind`）。
- 剧详情里的 `seasonBindings[]` 是 `View`：两种都有的字段，加上合集的（`adapter`、`link`、`sourceUrl`、`sourceLabel`、`numberedByRule`、`follow`、`status`、`lastError`、`lastCheckedAt`、`running`）或文件夹的（`entryCount`）。
- 详情是 `View` 加 `items`（按补建的语义派生，不删除）和 `emptySlots`；合集的条目带 `lastError`、`lastErrorAt`，文件夹的恒为 null。详情不请求平台。
- 预览是 `{candidates}`，每组带 `items`、`removed`（保存后删除的槽位）、`summary`。

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
- `useSeasonBindingPreview`（`use-season-bindings.ts`）让预览随表单自动刷新：
  - 查询键是防抖（`previewDebounce`，600ms）后的输入，不缓存，重新预览时保留上一次的结果。
  - `current` 为 false（还没防抖完、正在预览、预览失败）时不能保存；保存不比较表单改没改。
  - 有删除或替换时，保存前用确认框逐条列出要删的绑定，并说明替换的集偏移归零。
- 季绑定卡片（`SeasonBindingCard`）两种共用：
  - 卡片上只读地显示集号对应，集号规则和集号对应都在"编辑"里改。
  - 合集另有追更开关、状态、上次检查和"补建中"。
  - 删除的确认框写明建出的绑定会连同弹幕一起删除，文件夹另加"上传的文件也一起删除"。
  - `EntryTable` 用 `itemStatusText` 写出各条目的状态，上次失败的标红；表下用黄色提示列出 `emptySlots`。
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

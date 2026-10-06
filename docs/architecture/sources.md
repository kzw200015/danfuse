# 弹幕源与绑定

源适配器、合集与集号规则、弹幕文件，以及管理界面的绑定卡片。术语见 `GLOSSARY.md`，相关决策见 ADR 0004、0005。

## 源适配器

- 绑定存的是适配器 ID 加适配器自己的 ref（jsonb），ref 只交给适配器解析：绑定 JSON 里的 `sourceUrl`/`sourceLabel` 一律经适配器的 `Describe` 生成（`service.bindingView`），原始 ref 不对外输出。
- 源适配器只有 `source.Adapter` 一个接口，一个平台一个实现，方法都要实现，业务代码不做类型断言：
  - 弹幕源：`ParseLink`（集面板贴链接得到弹幕源 ref）、`Describe`、`Fetch`。
  - 合集：`ParseCollectionLink`（识别季面板的链接给出候选）、`ListCollection`（按合集 ref 列出条目）、`DescribeCollection`（生成展示的链接和标签）；没有合集的平台 `ParseCollectionLink` 一律返回 `source.ErrUnrecognized`。
  - 它只适用于按 ref 能重新拉取的弹幕源，弹幕文件不实现它。
- 合集 ref（`source.CollectionRef`）与弹幕源 ref 是两种类型；合集条目的弹幕源 ref 与单集绑定同一套格式，所以手动绑过的同一个弹幕源能被认出来。
- 集号对应（`source.Mapping`）、集号规则（`source.EpisodeRule`）、条目的整理（`source.NumberItems`）是 `source` 包里的纯计算，不在适配器里做：
  - 集号规则（ADR 0005）：投稿合集与多 P 投稿的适配器只给出展开到分 P 的条目和标签、标明 `Collection.NumberedByRule`，序号由季绑定上的规则从标签认出。规则是按优先级排列的一组正则，季绑定存它的副本；默认规则取自 `catalog.EpisodePatterns`，即 `ParseName` 那张表里带 `episode` 组的写法。
  - `NumberItems` 按规则认出序号，再交给 `NormalizeItems` 按 ref 去重、标出重复序号。
- B 站适配器的链接解析（`bilibili/link.go`）只做字符串分类（短链先跳转一次再分类），集面板与季面板各自决定接受哪些（集面板遇到合集的链接时提示到季面板）。
- 源适配器返回 `*source.Error`（带 Kind）：追更、重试、标为失效都按 Kind 分支；管理 API 由 `service.sourceAPIError` 按 Kind 转成 400/422/502，只包装 Err，日志里提示不重复。

## 弹幕文件（ADR 0004）

- 上传的 B 站 XML 弹幕文件建绑定，不实现 `source.Adapter`。`danmakufile` 领域包只做解析（XML 的解码与字段映射在 `danmaku/bilifmt`，B 站适配器共用）；业务在 `service/binding_file.go`（创建、追加文件、重新解析、文件列表）。
- 绑定的 `kind` 区分 `link` / `file`：文件绑定的 `adapter`、`ref`、`duration` 为空（CHECK 约束守住，唯一约束因此只作用于链接绑定）。
- 原文件存 `binding_files`（按 sha256 在绑定内去重），份数 `file_count` 与 `danmaku_count` 一样在事务里维护；弹幕不属于任何平台，原始 ID 直接用 dmid。
- `bindingView`、`bindingPlatform` 按 kind 分支，只对一种绑定有效的操作用在另一种上时返回 400。
- 上传的 handler 先给请求体套 `http.MaxBytesReader` 再解析 multipart，然后才 `bind`。

## 管理界面

- 创建绑定、重新拉取由后端当场拉取（最长约 25 秒），`bindings.ts` 里用 `slowRequestTimeout` 放宽超时，界面上用 `useElapsed` 显示已用秒数。
- 绑定卡片（`BindingCard`）按 `kind` 分出两组操作（`BindingSourceActions.tsx`：重新拉取 / 追加文件、重新解析），各自持有 mutation；一个绑定上改动弹幕的变更都带 `bindingKeys.write(id)` 前缀（`use-bindings.ts`），卡片用 `useIsMutating` 在任何一个进行中时禁用其他操作。
- 弹幕文件列表的键 `['binding-files', id]`，弹出层打开时才取。

## B 站适配器的测试

- 平时只回放 `internal/source/bilibili/testdata/` 里脱敏后的样本。测试里假 B 站换掉的是适配器 `http.Client` 的 Transport：各个域名（API、XML 弹幕、短链）的请求都转给它，按 Host 区分接口。
- live 模式请求真实的 B 站，默认关闭，CI 不请求：`go test ./internal/source/bilibili -run TestLive -args -live` 只验证；再加 `-update` 时先清空 `testdata/`，把响应脱敏后写进去（原始响应不落盘，限流、接口异常这类出错的响应不录制）。
- 在改动 B 站适配器之后和里程碑验收时各跑一遍，全部用例约 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速；以未登录的身份请求，港澳台限定番剧的用例要求从大陆的网络请求。
- 固定的公开视频列表（集面板的单集、季面板的合集）、脱敏规则见 `live_test.go` 开头，列表只挑内容中性的视频和番剧，状态变了（包括列表里连载中的番剧完结了）就换一个再重新录制。

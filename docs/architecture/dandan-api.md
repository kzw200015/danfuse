# 弹弹 API

弹弹play 协议的接口，供 jellyfin-danmaku 插件和支持自定义弹幕 API 的播放器使用。只查本地目录，不做上游聚合（ADR 0006）；播放时不向平台现取（ADR 0002）。

## 接口与分工

- 实现从识别、搜索到取弹幕的接口：`match` 按文件名识别到集；`search/episodes` 一步搜到季和集；`search/anime` 搜到季，再用 `bangumi/{bangumiId}` 取集；`comment` 取弹幕；`related` 是给插件的空兼容接口。对外的作品（animeId、bangumiId）就是季，episodeId 就是集。
- `dandan` 是一个领域包：`handler.go`（还有 `match.go`、`format.go`）只做协议参数转换和响应格式化（type、typeDescription、"第N话"、p、cid、平台前缀）；识别、搜索、取季与取弹幕交给 `service.go` 的 `dandan.Service`，它的查询（搜索季、取季和集）在 `dandandb/queries.sql`，读的是目录的表。
- `dandan.Service` 返回的类型（`dandan.Season`、`SearchResult`、`MatchResult` 等，季的类别用 `catalog.KindOf`）是内部结构，不含任何协议格式。
- `Match` 按名称识别一集：名称里要写明集号，其余交给同一个搜索，候选唯一且标题与剧名或原名 `fulltext.SameWords` 时才算确定（`isMatched`）。
- 搜索关键词和 match 的文件名按 `naming.Parse`（`catalog/naming`）认出季号、集号（规则见 [`catalog.md`](catalog.md)），集号以 `search/episodes` 的 `episode` 参数优先；按集号过滤时只返回有这一集的季，每季只带这一集（`Season.EpisodeCount` 仍是总集数）。
- 取弹幕时 `dandan.Service.Comments` 同时（errgroup）向 `binding.Service.EpisodeTracks` 要这一集各绑定的弹幕、向 `blockword.Service.Blocklist` 要一份编译好的屏蔽词（`danmaku.Blocklist`），再交给 `danmaku.Merge` 先去掉正文命中屏蔽词的，再做校正和跨源去重。`EpisodeTracks` 用一条查询读出这一集所有绑定落库的弹幕，平台取自绑定的适配器（弹幕文件为无平台）。`Blocklist` 每次都读表，内容与上次编译时相同就复用编译结果（`atomic.Pointer`），否则重新编译，所以改了屏蔽词（包括在别的实例上改的）下次取弹幕就生效；cid 由 `danmaku.CID` 在输出时现算，不存储。
- 屏蔽词的匹配规则在 `danmaku/blocklist.go`：关键词与正文都按跨源去重的同一套规则归一化后看是否包含，全部正则合成一条、对原始正文匹配。屏蔽只作用于弹弹 API 的输出，保存的弹幕和管理界面查看绑定的弹幕都不受影响。

## 响应格式

- 按官方 Swagger 的结构输出，不用 `response` 包：`comment` 只有 `{count, comments}`，其余为 ResponseBase 加数据字段。
- Swagger 里的字段一个不少，目录里没有的信息输出零值：不可空的为 false、0，列表任何时候都是 `[]`，其余为 null。
- 业务上的空返回 200 加空列表；`bangumi` 找不到作品时返回 200、`success:false`、`errorCode:404`、`bangumi:null`。
- 服务端故障时 `dandan` 的 handler 自己返回 500 和弹弹play 结构的响应体（`comment` 为 `{"count":0,"comments":[]}`，不加 `success`、`errorCode`），不交给 errorHandler，并调用 `response.LogServerError` 记日志；前缀下路由不匹配的 404、405 仍走全局处理。

## 路由

在 `internal/server/router.go` 的 `registerDandanRoutes` 里注册：

- 配置了 `dandanplay.token` 时前缀注册为 `/dandanplay/:token/api/v2`，由组中间件常数时间比较 token，不对时 404（5xx 日志里记的路由模式因此不含 token）。
- CORS（带 Origin 的请求回 `Access-Control-Allow-Origin: *`，允许 GET、POST（`match`），预检回显请求头）与 Gzip 只挂在这个路由组上，管理 API 不加。

## 测试

HTTP 测试在 `internal/server/dandan_test.go`（搜索、识别、取季的规则由 `internal/dandan/service_test.go` 覆盖，合并弹幕由 `internal/binding/danmaku_test.go` 覆盖）：

- 插件不调用的 `search/anime`、`bangumi`、`match` 按官方 Swagger 的结构断言完整 JSON；`TestSearchAnimeBangumiComment` 检查三步的 ID 前后衔接、两条搜索路径列出的集相同（`dandanPost` 发 `match` 的 POST；名称的解析规则在 `catalog/naming` 包的 `TestParse` 单独测）。
- 插件契约测试按 jellyfin-danmaku 插件实际的调用方式重放请求（`pluginGet` 跨域、带 `Accept-Encoding`，关键词不做 URL 编码、按浏览器的规则转义），按插件的读法断言（`pluginMatch` 取 `animes[0]`，按"集号 − 首集标题里第N话的 N"取集；`pluginComments` 把 `p` 按逗号拆成时间、模式、颜色、用户，按用户名前缀分来源）。
- 目录用 `syncCatalog` 经一次真实的同步写入，搜索列由同步核心算出，绑定与弹幕在同步之外用 SQL 补写（`newCommentServer`）。

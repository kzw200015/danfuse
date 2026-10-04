package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 插件契约测试：按 jellyfin-danmaku 插件实际的调用方式原样重放请求，按插件的读法断言结果。

// jellyfinOrigin 插件运行在 Jellyfin Web 的页面里，对 danfuse 的请求都是跨域的。
const jellyfinOrigin = "https://jellyfin.example.com"

// listSource 假目录源：依次产出 items。
type listSource []catalog.Item

func (s listSource) List(context.Context) (catalog.Listing, error) {
	return catalog.Listing{Total: len(s), Items: func(yield func(catalog.Item, error) bool) {
		for _, item := range s {
			if !yield(item, nil) {
				return
			}
		}
	}}, nil
}

// syncCatalog 用一次真实的同步把 items 写进 cfg 指向的库，搜索列由同步核心算出。
// 同步在 synctest 气泡里进行，连接池在气泡里创建和关闭。
func syncCatalog(t *testing.T, cfg *pgxpool.Config, items ...catalog.Item) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		svc := service.NewSyncService(repository.NewStore(pool), pool, listSource(items), config.Sync{}, slog.New(slog.DiscardHandler))
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			svc.Run(ctx)
		}()
		defer func() { cancel(); <-done }() // 在关闭连接池之前

		synctest.Wait()
		id, err := svc.Trigger(ctx)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if run, err := svc.GetRun(ctx, id); err != nil || run.Status != "succeeded" {
			t.Fatalf("同步没有成功：%+v, %v", run, err)
		}
	})
}

// pluginCatalog 契约测试用的目录。每个测试的库都从模板新建，同步按顺序写入，ID 从 1 开始（注释里标出）。
// 没有同名的剧：同名剧的自动匹配可能选错，是插件的已知限制。季都没有绑定。
func pluginCatalog() []catalog.Item {
	series := []*catalog.Series{
		{Type: catalog.TypeTV, Title: "星海旅人", OriginalTitle: "ほしうみの旅人", Year: new(2019), Seasons: []catalog.Season{
			{Number: 1, Episodes: []catalog.Episode{{Number: 1, Title: "启程"}, {Number: 2, Title: "归航"}}}, // 季 1：集 1、2
			{Number: 2, Episodes: []catalog.Episode{{Number: 13, Title: "新的航程"}, {Number: 14}}},          // 季 2：集 3、4，集号接着第 1 季往下数
			{Number: 0, Title: "Specials", Episodes: []catalog.Episode{{Number: 1, Title: "番外"}}},        // 季 3：集 5
		}},
		{Type: catalog.TypeTV, Title: "Night Watch", Year: new(2010), Seasons: []catalog.Season{
			{Number: 1, Episodes: []catalog.Episode{{Number: 1}, {Number: 2}}}, // 季 4：集 6、7
			{Number: 2, Episodes: []catalog.Episode{{Number: 1}}},              // 季 5：集 8
		}},
		{Type: catalog.TypeMovie, Title: "长夜灯塔", OriginalTitle: "Night Lighthouse", Year: new(2020), Seasons: []catalog.Season{
			{Number: 1, Episodes: []catalog.Episode{{Number: 1}}}, // 季 6：集 9
		}},
		{Type: catalog.TypeTV, Title: "无名之旅", Seasons: []catalog.Season{ // 没有年份
			{Number: 1, Episodes: []catalog.Episode{{Number: 1}}}, // 季 7：集 10
		}},
		{Type: catalog.TypeTV, Title: "Steins;Gate", Year: new(2011), Seasons: []catalog.Season{
			{Number: 1, Episodes: []catalog.Episode{{Number: 1}}}, // 季 8：集 11
		}},
	}
	items := make([]catalog.Item, len(series))
	for i, s := range series {
		items[i] = catalog.Item{Name: s.Title, Series: s}
	}
	return items
}

// biliAdapter 平台为 B 站的假适配器：读取弹幕只用到适配器的 ID 和平台，调用其他方法会 panic。
type biliAdapter struct{ source.Adapter }

func (biliAdapter) ID() string                 { return "bilibili" }
func (biliAdapter) Platform() danmaku.Platform { return danmaku.PlatformBilibili }

// dandanServer 起完整的 Echo，弹弹 API 连到 pool；日志写进 logs（为 nil 时丢弃）。
// 源适配器注册了 biliAdapter（B 站平台）与 fakeAdapter（没有平台）。
func dandanServer(pool *pgxpool.Pool, token string, logs io.Writer) *Server {
	if logs == nil {
		logs = io.Discard
	}
	local := service.NewLocalProvider(repository.NewStore(pool), source.NewRegistry(biliAdapter{}, fakeAdapter{}))
	dh := dandan.NewHandler(provider.NewAggregator(local))
	return New(config.Server{}, config.Dandanplay{Token: token}, slog.New(slog.NewJSONHandler(logs, nil)),
		&handler.Handlers{Health: handler.NewHealthHandler(pool)}, dh)
}

// newDandanServer 新建一个库，用同步写入 items，再起弹弹 API。要在同步之外补写数据（例如绑定和弹幕）时，
// 照这里的写法组合 dbtest.Config、syncCatalog、pgxpool.NewWithConfig 和 dandanServer，自己留着连接池。
func newDandanServer(t *testing.T, token string, items ...catalog.Item) *Server {
	t.Helper()
	cfg := dbtest.Config(t)
	syncCatalog(t, cfg, items...)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return dandanServer(pool, token, nil)
}

// browserURL 插件直接把关键词拼进地址，不做 URL 编码（ede.js 的 getEpisodeInfo），由浏览器按 URL 标准处理：
// # 之后是片段，不发给服务端；查询串里空白、引号、尖括号和非 ASCII 字符按 UTF-8 百分号编码，& + = 原样保留。
func browserURL(raw string) string {
	raw, _, _ = strings.Cut(raw, "#")
	var b strings.Builder
	for _, c := range []byte(raw) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`"'<>`, c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pluginGet 像插件在浏览器里那样发一个 GET：跨域，带 Accept 和浏览器自己加的 Accept-Encoding；
// header 是额外的请求头。返回响应和解压后的响应体。
func pluginGet(t *testing.T, srv *Server, target string, header http.Header) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, browserURL(target), nil)
	req.Header.Set("Origin", jellyfinOrigin)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	body := rec.Body.Bytes()
	if rec.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("GET %s: 响应不是 gzip：%v", target, err)
		}
		if body, err = io.ReadAll(zr); err != nil {
			t.Fatalf("GET %s: 解压响应：%v", target, err)
		}
	}
	return rec, body
}

// pluginAnime 插件从 search/episodes 的响应里读的字段。
type pluginAnime struct {
	AnimeID         int64  `json:"animeId"`
	AnimeTitle      string `json:"animeTitle"`
	Type            string `json:"type"`
	TypeDescription string `json:"typeDescription"`
	Episodes        []struct {
		EpisodeID    int64  `json:"episodeId"`
		EpisodeTitle string `json:"episodeTitle"`
	} `json:"episodes"`
}

// searchEpisodes 插件的搜索请求：base 后固定拼 /api/v2，关键词原样拼进查询串。
func searchEpisodes(t *testing.T, srv *Server, base, keyword string) []pluginAnime {
	t.Helper()
	rec, body := pluginGet(t, srv, base+"/api/v2/search/episodes?anime="+keyword, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("搜索 %q：status = %d, body %s", keyword, rec.Code, body)
	}
	var resp struct {
		Animes []pluginAnime `json:"animes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("搜索 %q：响应不是 JSON：%s", keyword, body)
	}
	return resp.Animes
}

// jellyfinItem 插件从 Jellyfin 读到的正在播放的条目。
type jellyfinItem struct {
	SeriesName    string // 电影没有 SeriesName，插件用 Name
	OriginalTitle string
	Season        int // ParentIndexNumber，电影为 0
	Episode       int // IndexNumber，电影为 1
}

var episodeNumberPattern = regexp.MustCompile(`第(\d+)话`)

// pluginMatch 插件自动匹配一集（ede.js 的 getEpisodeInfo）：关键词取剧名，第 2 季起直接在后面拼季号；
// 搜不到时改用原名再搜一次，不带季号。选 animes[0]，按"集号 − initialep"作为下标取集，
// initialep 从首集标题的"第N话"解析，解析不到时为 1。返回选中的季和集，没有匹配上时为 0。
func pluginMatch(t *testing.T, srv *Server, base string, item jellyfinItem) (animeID, episodeID int64) {
	t.Helper()
	keyword := item.SeriesName
	if item.Season > 1 {
		keyword += strconv.Itoa(item.Season)
	}
	animes := searchEpisodes(t, srv, base, keyword)
	if len(animes) == 0 {
		animes = searchEpisodes(t, srv, base, item.OriginalTitle)
	}
	if len(animes) == 0 {
		return 0, 0
	}
	anime := animes[0]
	initialEp := 1
	if m := episodeNumberPattern.FindStringSubmatch(anime.Episodes[0].EpisodeTitle); m != nil {
		initialEp, _ = strconv.Atoi(m[1])
	}
	index := item.Episode - initialEp
	if item.Episode < initialEp {
		index = item.Episode - 1
	}
	if index+1 > len(anime.Episodes) {
		return anime.AnimeID, 0
	}
	return anime.AnimeID, anime.Episodes[index].EpisodeID
}

func TestPluginMatchesEpisode(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	tests := []struct {
		name          string
		item          jellyfinItem
		wantAnimeID   int64
		wantEpisodeID int64
	}{
		{"第 1 季", jellyfinItem{"星海旅人", "ほしうみの旅人", 1, 2}, 1, 2},
		{"第 2 季起拼上季号，首集不是第 1 话", jellyfinItem{"星海旅人", "ほしうみの旅人", 2, 14}, 2, 4},
		{"剧名带空格，原样拼进地址", jellyfinItem{"Night Watch", "", 2, 1}, 5, 8},
		{"电影", jellyfinItem{"长夜灯塔", "Night Lighthouse", 0, 1}, 6, 9},
		// 例如 Jellyfin 里改了剧名、还没同步：插件改用原名再搜一次
		{"剧名搜不到时用原名回退", jellyfinItem{"星海の旅人", "ほしうみの旅人", 1, 1}, 1, 1},
		// 浏览器不转义 ;，表单编码里它也不是分隔符
		{"剧名带分号", jellyfinItem{"Steins;Gate", "", 1, 1}, 8, 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			animeID, episodeID := pluginMatch(t, srv, "/dandanplay", tt.item)
			if animeID != tt.wantAnimeID || episodeID != tt.wantEpisodeID {
				t.Errorf("选中季 %d 集 %d, want 季 %d 集 %d", animeID, episodeID, tt.wantAnimeID, tt.wantEpisodeID)
			}
		})
	}
}

// TestPluginManualSearch 手动搜索时插件列出 animeTitle 和 typeDescription；手动选过的季，
// 插件之后拿我们返回的 animeTitle 作为这一季其他集的关键词。
func TestPluginManualSearch(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	for _, tt := range []struct {
		keyword     string
		wantAnimeID int64
	}{
		{"星海旅人 特别篇", 3},
		{"星海旅人 第2季", 2},
		{"星海旅人", 1},
		{"旅人", 1},
		{"night", 4}, // 剧名命中（Night Watch）排在原名命中（长夜灯塔）之前
	} {
		t.Run(tt.keyword, func(t *testing.T) {
			animes := searchEpisodes(t, srv, "/dandanplay", tt.keyword)
			if len(animes) == 0 || animes[0].AnimeID != tt.wantAnimeID {
				t.Errorf("animes = %+v, want animes[0] 为季 %d", animes, tt.wantAnimeID)
			}
		})
	}
}

func TestSearchEpisodesResponse(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	empty := `{"errorCode": 0, "success": true, "errorMessage": null, "errorDetail": null, "hasMore": false, "animes": []}`
	tests := []struct {
		name    string
		keyword string
		want    string
	}{
		{
			// 第 1 季为"剧名"，第 N 季为"剧名 第N季"，特别篇排在最后；集按集号升序，没有集标题时只写"第N话"
			"剧集", "星海旅人", `{"errorCode": 0, "success": true, "errorMessage": null, "errorDetail": null, "hasMore": false, "animes": [
				{"animeId": 1, "animeTitle": "星海旅人", "type": "tvseries", "typeDescription": "剧集 · 2019", "episodes": [
					{"episodeId": 1, "episodeTitle": "第1话 启程"}, {"episodeId": 2, "episodeTitle": "第2话 归航"}]},
				{"animeId": 2, "animeTitle": "星海旅人 第2季", "type": "tvseries", "typeDescription": "剧集 · 2019", "episodes": [
					{"episodeId": 3, "episodeTitle": "第13话 新的航程"}, {"episodeId": 4, "episodeTitle": "第14话"}]},
				{"animeId": 3, "animeTitle": "星海旅人 特别篇", "type": "tvspecial", "typeDescription": "特别篇 · 2019", "episodes": [
					{"episodeId": 5, "episodeTitle": "第1话 番外"}]}
			]}`,
		},
		{
			"电影", "长夜灯塔", `{"errorCode": 0, "success": true, "errorMessage": null, "errorDetail": null, "hasMore": false, "animes": [
				{"animeId": 6, "animeTitle": "长夜灯塔", "type": "movie", "typeDescription": "电影 · 2020", "episodes": [
					{"episodeId": 9, "episodeTitle": "第1话"}]}
			]}`,
		},
		{
			"没有年份时省略年份", "无名之旅", `{"errorCode": 0, "success": true, "errorMessage": null, "errorDetail": null, "hasMore": false, "animes": [
				{"animeId": 7, "animeTitle": "无名之旅", "type": "tvseries", "typeDescription": "剧集", "episodes": [
					{"episodeId": 10, "episodeTitle": "第1话"}]}
			]}`,
		},
		{"搜不到", "搜不到", empty},
		{"不足 2 个字符", "星", empty},
		{"去掉首尾空白后不足 2 个字符", " 星 ", empty},
		{"切不出词", "！？", empty},
		{"空关键词", "", empty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := pluginGet(t, srv, "/dandanplay/api/v2/search/episodes?anime="+tt.keyword, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			assertJSON(t, body, tt.want)
		})
	}

	// 没有 anime 参数；episode、tmdbId 忽略
	for _, target := range []string{"/dandanplay/api/v2/search/episodes", "/dandanplay/api/v2/search/episodes?tmdbId=1&episode=1"} {
		_, body := pluginGet(t, srv, target, nil)
		assertJSON(t, body, empty)
	}
}

func TestSearchEpisodesHasMore(t *testing.T) {
	t.Parallel()
	seasons := make([]catalog.Season, 51)
	for i := range seasons {
		seasons[i] = catalog.Season{Number: i + 1, Episodes: []catalog.Episode{{Number: 1}}}
	}
	srv := newDandanServer(t, "", catalog.Item{Name: "长篇连载", Series: &catalog.Series{Type: catalog.TypeTV, Title: "长篇连载", Seasons: seasons}})

	_, body := pluginGet(t, srv, "/dandanplay/api/v2/search/episodes?anime=长篇连载", nil)
	var resp struct {
		HasMore bool              `json:"hasMore"`
		Animes  []json.RawMessage `json:"animes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Animes) != 50 || !resp.HasMore {
		t.Errorf("返回 %d 季，hasMore %v；want 50 季，hasMore true", len(resp.Animes), resp.HasMore)
	}
}

// newCommentServer 契约测试用的目录（pluginCatalog）加上星海旅人第 1 集（集 1）的绑定与弹幕，起弹弹 API。
// 绑定与弹幕在同步之外补写。
func newCommentServer(t *testing.T) *Server {
	t.Helper()
	cfg := dbtest.Config(t)
	syncCatalog(t, cfg, pluginCatalog()...)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", status, danmaku_count) VALUES
			(1, 'bilibili', '{"kind": "video", "aid": 1, "page": 1}', '星海旅人 / 第 1 话', 1420, 0, 'active', 4), -- 绑定 1
			(1, 'bilibili', '{"kind": "episode", "epId": 2}', '星海旅人 启程', 1422, 10, 'dead', 3),          -- 绑定 2：失效，弹幕延后 10 秒
			(1, 'fake', '{"name": "local"}', '没有平台的来源', 1420, 0, 'active', 1);                          -- 绑定 3
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES
			(1, 1983745621937266688, 0, 1, 16777215, '前排'),
			(1, 102, 61000, 6, 15138834, '逆向'),
			(1, 103, 120500, 4, 0, '底部'),
			(1, 104, 130000, 5, 255, '顶部 &<>"'),
			(2, 1983745621937266688, 0, 1, 16777215, '前排'), -- 原始 ID 与绑定 1 的那条相同；校正后相差 10 秒，只靠按 ID 去掉
			(2, 201, 51500, 1, 0, '逆向'),                    -- 校正后 61.5 秒，与绑定 1 的那条相差 0.5 秒，按文本去掉
			(2, 202, 300000, 1, 16777215, '失效绑定的弹幕'),
			(3, 1, 5000, 1, 0, '没有平台');`)
	if err != nil {
		t.Fatal(err)
	}
	return dandanServer(pool, "", nil)
}

// pluginComment 插件从 comment 的响应里读出的一条弹幕。
type pluginComment struct {
	Time   float64 // 秒
	Mode   int
	Color  int
	Source string // 插件按用户名前缀分的来源
	Text   string
}

// pluginComments 插件取一集的弹幕（ede.js 的 getComments 与 preProcessDanmaku）：固定带 withRelated=true 和用户设置的
// chConvert，只读 comments[].p 和 m。p 按逗号拆成时间、模式、颜色、用户四段，时间按浮点数、模式和颜色按整数读出，
// 用户名前缀决定来源。插件只认模式 1、4、5、6，协议只定义了 1、4、5，出现别的模式时测试失败。
func pluginComments(t *testing.T, srv *Server, base string, episodeID int64) []pluginComment {
	t.Helper()
	target := fmt.Sprintf("%s/api/v2/comment/%d?withRelated=true&chConvert=1", base, episodeID)
	rec, body := pluginGet(t, srv, target, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body %s", target, rec.Code, body)
	}
	var resp struct {
		Comments []struct {
			P string `json:"p"`
			M string `json:"m"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Comments == nil {
		t.Fatalf("GET %s: 读不到 comments（插件会显示无弹幕）：%s", target, body)
	}

	var result []pluginComment
	for _, c := range resp.Comments {
		parts := strings.Split(c.P, ",")
		if len(parts) != 4 {
			t.Fatalf("p = %q，want 时间、模式、颜色、用户四段", c.P)
		}
		seconds, errTime := strconv.ParseFloat(parts[0], 64)
		mode, errMode := strconv.Atoi(parts[1])
		color, errColor := strconv.Atoi(parts[2])
		if err := errors.Join(errTime, errMode, errColor); err != nil {
			t.Fatalf("p = %q：%v", c.P, err)
		}
		if !slices.Contains([]int{1, 4, 5}, mode) {
			t.Errorf("p = %q：模式 %d，want 只有 1、4、5", c.P, mode)
		}
		result = append(result, pluginComment{Time: seconds, Mode: mode, Color: color, Source: pluginSource(parts[3]), Text: c.M})
	}
	return result
}

// pluginSource 插件按用户名前缀分来源：[BiliBili] 是 B 站，[Gamer] 是巴哈，不以 [ 开头的是弹弹，其余是其他。
func pluginSource(user string) string {
	switch {
	case strings.HasPrefix(user, "[BiliBili]"):
		return "B 站"
	case strings.HasPrefix(user, "[Gamer]"):
		return "巴哈"
	case !strings.HasPrefix(user, "["):
		return "弹弹"
	default:
		return "其他"
	}
}

// TestPluginComments 插件自动匹配到一集后取弹幕：所有绑定校正、去重后合并，B 站的弹幕归为 B 站来源，
// 失效绑定的弹幕照常输出，逆向弹幕输出为滚动。
func TestPluginComments(t *testing.T) {
	t.Parallel()
	srv := newCommentServer(t)

	_, episodeID := pluginMatch(t, srv, "/dandanplay", jellyfinItem{"星海旅人", "ほしうみの旅人", 1, 1})
	got := pluginComments(t, srv, "/dandanplay", episodeID)

	want := []pluginComment{
		{0, 1, 0xFFFFFF, "B 站", "前排"},
		{5, 1, 0, "弹弹", "没有平台"},
		{61, 1, 0xE70012, "B 站", "逆向"},
		{120.5, 4, 0, "B 站", "底部"},
		{130, 5, 0xFF, "B 站", `顶部 &<>"`},
		{310, 1, 0xFFFFFF, "B 站", "失效绑定的弹幕"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("集 %d 的弹幕 = %+v\nwant %+v", episodeID, got, want)
	}
}

func TestCommentResponse(t *testing.T) {
	t.Parallel()
	srv := newCommentServer(t)

	// cid 由平台与原始 ID 算出，与 danmaku.CID 的单元测试固定的值一致
	full := `{"count": 6, "comments": [
		{"cid": 1881586332506557, "p": "0.00,1,16777215,[BiliBili]", "m": "前排"},
		{"cid": 494350949496718, "p": "5.00,1,0,", "m": "没有平台"},
		{"cid": 3479445432466440, "p": "61.00,1,15138834,[BiliBili]", "m": "逆向"},
		{"cid": 8283615148896597, "p": "120.50,4,0,[BiliBili]", "m": "底部"},
		{"cid": 1657130281072079, "p": "130.00,5,255,[BiliBili]", "m": "顶部 &<>\""},
		{"cid": 6135997043884386, "p": "310.00,1,16777215,[BiliBili]", "m": "失效绑定的弹幕"}
	]}`
	empty := `{"count": 0, "comments": []}`
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{"插件的请求", "/comment/1?withRelated=true&chConvert=1", full},
		{"withRelated、from、chConvert 忽略", "/comment/1?withRelated=false&from=5&chConvert=2", full},
		{"没有参数", "/comment/1", full},
		{"没有绑定的集", "/comment/2", empty},
		{"不存在的集", "/comment/999999", empty},
		{"本地号段以外的 ID", "/comment/10000000000000", empty},
		{"不是整数的 ID", "/comment/abc", empty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := pluginGet(t, srv, "/dandanplay/api/v2"+tt.target, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestRelated(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	// 插件只读 relateds[].url，为空就不会再调 extcomment
	for _, target := range []string{"/dandanplay/api/v2/related/1", "/dandanplay/api/v2/related/999999"} {
		rec, body := pluginGet(t, srv, target, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", target, rec.Code)
		}
		assertJSON(t, body, `{"errorCode": 0, "success": true, "errorMessage": null, "errorDetail": null, "relateds": []}`)
	}
}

func TestDandanUnregisteredEndpoints(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	for _, target := range []string{
		"/dandanplay/api/v2/extcomment?chConvert=0&url=https://www.bilibili.com/video/BV1xx411c7XX",
		"/dandanplay/api/v2/bangumi/1",
		"/dandanplay/api/v2/match",
		"/dandanplay/api/v2/search/anime?keyword=星海旅人",
	} {
		rec, body := pluginGet(t, srv, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
		assertJSON(t, body, `{"code": 1, "message": "Not Found", "data": null}`) // 前缀下没命中的路由走全局处理
	}
}

func TestDandanCORS(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "", pluginCatalog()...)

	// 带 Origin 的请求（浏览器的跨域请求都带），响应都带 Access-Control-Allow-Origin，包括出错的
	for _, target := range []string{
		"/dandanplay/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/api/v2/comment/1?withRelated=true&chConvert=1",
		"/dandanplay/api/v2/related/1",
		"/dandanplay/api/v2/match",
	} {
		if rec, _ := pluginGet(t, srv, target, nil); rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("GET %s: Access-Control-Allow-Origin = %q, want *", target, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}

	// 插件设置了 User-Agent 请求头，保留它的浏览器会先发预检
	for _, target := range []string{
		"/dandanplay/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/api/v2/comment/1?withRelated=true&chConvert=1",
		"/dandanplay/api/v2/related/1",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, browserURL(target), nil)
		req.Header.Set("Origin", jellyfinOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "user-agent")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("OPTIONS %s: status = %d, want 204", target, rec.Code)
		}
		h := rec.Header()
		if got := strings.ReplaceAll(h.Get("Access-Control-Allow-Methods"), " ", ""); got != "GET,OPTIONS" {
			t.Errorf("OPTIONS %s: Allow-Methods = %q, want GET, OPTIONS", target, h.Get("Access-Control-Allow-Methods"))
		}
		for name, want := range map[string]string{
			"Access-Control-Allow-Origin":      "*",
			"Access-Control-Allow-Headers":     "user-agent", // 回显请求的 Access-Control-Request-Headers
			"Access-Control-Max-Age":           "86400",
			"Access-Control-Allow-Credentials": "",
		} {
			if got := h.Get(name); got != want {
				t.Errorf("OPTIONS %s: %s = %q, want %q", target, name, got, want)
			}
		}
	}

	// 管理 API 不加 CORS
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", jellyfinOrigin)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); rec.Code != http.StatusOK || got != "" {
		t.Errorf("GET /api/health: status = %d, Access-Control-Allow-Origin = %q, want 200 且没有 CORS 头", rec.Code, got)
	}
}

func TestDandanGzip(t *testing.T) {
	t.Parallel()
	srv := newCommentServer(t)

	for _, target := range []string{
		"/dandanplay/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/api/v2/comment/1?withRelated=true&chConvert=1",
		"/dandanplay/api/v2/related/1",
	} {
		rec, body := pluginGet(t, srv, target, nil)
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Errorf("GET %s: Content-Encoding = %q, want gzip", target, got)
		}
		if !json.Valid(body) {
			t.Errorf("GET %s: 解压后不是 JSON：%s", target, body)
		}
	}
}

func TestDandanToken(t *testing.T) {
	t.Parallel()
	srv := newDandanServer(t, "s3cret", pluginCatalog()...)

	if animeID, episodeID := pluginMatch(t, srv, "/dandanplay/s3cret", jellyfinItem{"星海旅人", "", 2, 13}); animeID != 2 || episodeID != 3 {
		t.Errorf("带 token 的地址：选中季 %d 集 %d, want 季 2 集 3", animeID, episodeID)
	}
	if rec, _ := pluginGet(t, srv, "/dandanplay/s3cret/api/v2/related/1", nil); rec.Code != http.StatusOK {
		t.Errorf("带 token 的 related: status = %d, want 200", rec.Code)
	}
	for _, target := range []string{
		"/dandanplay/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/wrong/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/S3CRET/api/v2/related/1",
		"/dandanplay/s3cre/api/v2/related/1",
	} {
		rec, body := pluginGet(t, srv, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
		assertJSON(t, body, `{"code": 1, "message": "Not Found", "data": null}`)
	}
}

// TestDandanIgnoresAppHeaders 官方的鉴权头（插件默认的 CORS 代理会注入）一律忽略，带与不带响应相同。
func TestDandanIgnoresAppHeaders(t *testing.T) {
	t.Parallel()
	srv := newCommentServer(t)
	appHeaders := http.Header{
		"X-AppId":     {"app"},
		"X-AppSecret": {"secret"},
		"X-Timestamp": {"1759536000"},
		"X-Signature": {"c2lnbmF0dXJl"},
	}

	for _, target := range []string{
		"/dandanplay/api/v2/search/episodes?anime=星海旅人",
		"/dandanplay/api/v2/comment/1?withRelated=true&chConvert=1",
		"/dandanplay/api/v2/related/1",
	} {
		plain, plainBody := pluginGet(t, srv, target, nil)
		withApp, withAppBody := pluginGet(t, srv, target, appHeaders)
		if withApp.Code != plain.Code || !bytes.Equal(withAppBody, plainBody) {
			t.Errorf("GET %s: 带 X-App* 头时 %d %s\n不带时 %d %s", target, withApp.Code, withAppBody, plain.Code, plainBody)
		}
	}
}

// TestDandanServerError 服务端故障返回 500 和弹弹play 结构的响应体（各接口按官方 Swagger 的 schema），
// 按全局 errorHandler 的字段记日志；日志里的路由是注册时的模式，不含 token。
func TestDandanServerError(t *testing.T) {
	t.Parallel()
	const token = "s3cret"
	pool := dbtest.Pool(t)
	pool.Close() // 数据库不可用

	tests := []struct {
		target    string
		wantRoute string
		wantBody  string
	}{
		{
			"/search/episodes?anime=星海旅人", "/dandanplay/:token/api/v2/search/episodes",
			`{"errorCode": 1, "success": false, "errorMessage": "服务器内部错误", "errorDetail": null, "hasMore": false, "animes": []}`,
		},
		// 官方的 CommentResponseV2 只有 count 和 comments，不加 success、errorCode
		{"/comment/1?withRelated=true&chConvert=1", "/dandanplay/:token/api/v2/comment/:episodeId", `{"count": 0, "comments": []}`},
	}
	for _, tt := range tests {
		t.Run(tt.wantRoute, func(t *testing.T) {
			var logs bytes.Buffer
			srv := dandanServer(pool, token, &logs)

			rec, body := pluginGet(t, srv, "/dandanplay/"+token+"/api/v2"+tt.target, nil)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rec.Code)
			}
			assertJSON(t, body, tt.wantBody)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
			}

			var entry struct {
				Level     string `json:"level"`
				RequestID string `json:"request_id"`
				Method    string `json:"method"`
				Route     string `json:"route"`
				Error     string `json:"error"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("应恰好记录一条日志：%v\n%s", err, logs.String())
			}
			if want := rec.Header().Get("X-Request-Id"); entry.Level != "ERROR" || entry.RequestID == "" || entry.RequestID != want ||
				entry.Method != http.MethodGet || entry.Route != tt.wantRoute || entry.Error == "" {
				t.Errorf("日志 = %+v, want ERROR、request_id %q、GET、路由模式 %s 和错误链", entry, want, tt.wantRoute)
			}
			if strings.Contains(logs.String(), token) {
				t.Errorf("日志里出现了 token：%s", logs.String())
			}
		})
	}
}

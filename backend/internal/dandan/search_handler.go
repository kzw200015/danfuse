package dandan

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
)

// searchEpisodesResponse 官方 Swagger 的 SearchEpisodesResponse。
type searchEpisodesResponse struct {
	responseBase
	HasMore bool    `json:"hasMore"`
	Animes  []anime `json:"animes"` // 插件不判空，任何时候都输出 []
}

// anime 官方的 SearchEpisodesAnime：一个作品，对应一季。
type anime struct {
	AnimeID         int64     `json:"animeId"`
	AnimeTitle      string    `json:"animeTitle"`
	Type            string    `json:"type"`
	TypeDescription string    `json:"typeDescription"`
	Episodes        []episode `json:"episodes"`
}

// episode 官方的 SearchEpisodeDetails。
type episode struct {
	EpisodeID    int64  `json:"episodeId"`
	EpisodeTitle string `json:"episodeTitle"`
}

// SearchEpisodes GET search/episodes?anime=&episode=
// anime 的规则见 searchKeyword，其中写明的季号、集号只要这一季、这一集（naming.Parse）。episode 为正整数时
// 只保留这一集，优先于 anime 里写明的集号；其他值（包括 C1、S1、O1 这样的格式）忽略。没有给 episode 时，
// 协议把 anime 里空格后面的数字当作集号，这里不这样做：没有标注的数字分不出是标题的一部分、季号还是集号。
// 最多返回 50 季，被截断时 hasMore 为 true。tmdbId 忽略。插件自动匹配时取 animes[0]，再按数组下标取集。
func (h *Handler) SearchEpisodes(c *echo.Context) error {
	start := time.Now()
	resp := searchEpisodesResponse{responseBase: succeeded, Animes: []anime{}}
	query := formQuery(c)
	keyword, ok := searchKeyword(query, "anime")
	var episode *int
	if n, err := strconv.Atoi(query.Get("episode")); err == nil && n > 0 {
		episode = &n
	}
	if !ok {
		logSearch(c, start, "search/episodes", keyword, episode, 0, false)
		return c.JSON(http.StatusOK, resp)
	}

	result, err := h.svc.Search(c.Request().Context(), SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons, Episode: episode})
	if err != nil {
		return serverError(c, err, searchEpisodesResponse{responseBase: failed, Animes: []anime{}})
	}
	resp.HasMore = result.HasMore
	for _, s := range result.Seasons {
		resp.Animes = append(resp.Animes, toAnime(s))
	}
	logSearch(c, start, "search/episodes", keyword, episode, len(result.Seasons), result.HasMore)
	return c.JSON(http.StatusOK, resp)
}

// searchAnimeResponse 官方的 SearchAnimeResponse：没有 hasMore。
type searchAnimeResponse struct {
	responseBase
	Animes []searchAnime `json:"animes"`
}

// searchAnime 官方的 SearchAnimeDetails：一个作品（一季），不带集。目录里没有的信息按 Swagger 的类型输出：
// 海报不经弹弹 API 提供、目录只有年份不编造上映日期，所以 imageUrl、startDate 为 null；没有评分和用户，rating 为 0、isFavorited 为 false。
type searchAnime struct {
	AnimeID         int64      `json:"animeId"`
	BangumiID       string     `json:"bangumiId"`
	AnimeTitle      string     `json:"animeTitle"`
	Type            string     `json:"type"`
	TypeDescription string     `json:"typeDescription"`
	ImageURL        *string    `json:"imageUrl"`
	StartDate       *time.Time `json:"startDate"`
	EpisodeCount    int        `json:"episodeCount"`
	Rating          float64    `json:"rating"`
	IsFavorited     bool       `json:"isFavorited"`
}

// SearchAnime GET search/anime?keyword=
// 与 search/episodes 是同一个搜索，keyword 的规则也相同（见 searchKeyword）；空格分开的词都要命中，与协议的说明一致。
// 结果是季，不带集，只给总集数，客户端再用 bangumiId 取作品详情。协议的响应没有 hasMore，最多返回 50 季，多出的截掉。
// type、v2 忽略。
func (h *Handler) SearchAnime(c *echo.Context) error {
	start := time.Now()
	resp := searchAnimeResponse{responseBase: succeeded, Animes: []searchAnime{}}
	keyword, ok := searchKeyword(formQuery(c), "keyword")
	if !ok {
		logSearch(c, start, "search/anime", keyword, nil, 0, false)
		return c.JSON(http.StatusOK, resp)
	}

	result, err := h.svc.Search(c.Request().Context(), SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons})
	if err != nil {
		return serverError(c, err, searchAnimeResponse{responseBase: failed, Animes: []searchAnime{}})
	}
	for _, s := range result.Seasons {
		resp.Animes = append(resp.Animes, toSearchAnime(s))
	}
	logSearch(c, start, "search/anime", keyword, nil, len(result.Seasons), result.HasMore)
	return c.JSON(http.StatusOK, resp)
}

// formQuery 按表单编码的规则解析查询串：有的客户端（例如 jellyfin-danmaku 插件）不对关键词做 URL 编码，
// 标题里的 & # + 会把它截断或变形，不做补救。
func formQuery(c *echo.Context) url.Values {
	// 表单编码里只有 & 是分隔符，; 是普通字符（例如 Steins;Gate）；Go 的 url.ParseQuery 出于自己的安全取舍
	// 会丢掉含 ; 的整个参数，所以先把它转义。解析出错的参数照样丢掉，与 URL.Query 一致
	query, _ := url.ParseQuery(strings.ReplaceAll(c.Request().URL.RawQuery, ";", "%3B"))
	return query
}

// logSearch 搜索的日志：关键词原文、episode 参数，返回的季数和后面还有没有（search/anime 截掉的部分也算）。
// 关键词不足 2 个字符时照样记，季数为 0。
func logSearch(c *echo.Context, start time.Time, api, keyword string, episode *int, seasons int, hasMore bool) {
	logRequest(c, start, "dandanplay search",
		slog.String("api", api), slog.String("keyword", keyword), optionalInt("episode", episode),
		slog.Int("seasons", seasons), slog.Bool("has_more", hasMore))
}

// searchKeyword 取参数 name 作为搜索关键词，去掉首尾空白；不足 2 个字符时 ok 为 false，调用方直接返回空列表（协议规则）。
func searchKeyword(query url.Values, name string) (keyword string, ok bool) {
	keyword = strings.TrimSpace(query.Get(name))
	return keyword, utf8.RuneCountInString(keyword) >= minKeywordLen
}

// toAnime 一季转成 search/episodes 里的作品。集按 service 给的顺序（集号升序）输出，不补占位：
// 插件按"集号 − 首集标题里的 N"作为下标取集，目录中间缺集时会错位。
func toAnime(s Season) anime {
	typ, description := typeOf(s)
	a := anime{
		AnimeID:         s.ID,
		AnimeTitle:      s.Name,
		Type:            typ,
		TypeDescription: description,
		Episodes:        make([]episode, len(s.Episodes)),
	}
	for i, e := range s.Episodes {
		a.Episodes[i] = episode{EpisodeID: e.ID, EpisodeTitle: episodeTitle(e)}
	}
	return a
}

// toSearchAnime 一季转成 search/anime 里的作品：不带集，只给总集数。bangumiId 是同一个 ID 的字符串。
func toSearchAnime(s Season) searchAnime {
	typ, description := typeOf(s)
	return searchAnime{
		AnimeID:         s.ID,
		BangumiID:       strconv.FormatInt(s.ID, 10),
		AnimeTitle:      s.Name,
		Type:            typ,
		TypeDescription: description,
		EpisodeCount:    s.EpisodeCount,
	}
}

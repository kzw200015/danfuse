package dandan

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
)

// bangumiResponse 官方的 BangumiDetailsResponse：作品不存在时 bangumi 为 null。
type bangumiResponse struct {
	responseBase
	Bangumi *bangumiDetails `json:"bangumi"`
}

// bangumiDetails 官方的 BangumiDetails，字段一个不少，按完整 schema 解码的客户端也能读。目录里没有的信息
// （海报、简介、评分、标签、关联作品等）按 Swagger 的类型输出零值：不可空的为 false、0，列表为 []，ratingDetails 为 {}，其余为 null。
type bangumiDetails struct {
	AnimeID         int64              `json:"animeId"`
	BangumiID       string             `json:"bangumiId"`
	AnimeTitle      string             `json:"animeTitle"`
	ImageURL        *string            `json:"imageUrl"`
	SearchKeyword   *string            `json:"searchKeyword"`
	IsOnAir         bool               `json:"isOnAir"`
	AirDay          int                `json:"airDay"`
	IsFavorited     bool               `json:"isFavorited"`
	IsRestricted    bool               `json:"isRestricted"`
	Rating          float64            `json:"rating"`
	Type            string             `json:"type"`
	TypeDescription string             `json:"typeDescription"`
	Titles          []struct{}         `json:"titles"`
	Seasons         []struct{}         `json:"seasons"`
	Episodes        []bangumiEpisode   `json:"episodes"`
	Summary         *string            `json:"summary"`
	Intro           *string            `json:"intro"`
	Metadata        []string           `json:"metadata"`
	BangumiURL      *string            `json:"bangumiUrl"`
	UserRating      int                `json:"userRating"`
	FavoriteStatus  *string            `json:"favoriteStatus"`
	Comment         *string            `json:"comment"`
	RatingDetails   map[string]float64 `json:"ratingDetails"`
	Relateds        []struct{}         `json:"relateds"`
	Similars        []struct{}         `json:"similars"`
	Tags            []struct{}         `json:"tags"`
	OnlineDatabases []struct{}         `json:"onlineDatabases"`
	Trailers        []struct{}         `json:"trailers"`
}

// bangumiEpisode 官方的 BangumiEpisode。一个作品就是一季，seasonId 为 null（协议：为空表示只有一季）；
// 目录里没有放送日期和观看记录，airDate、lastWatched 为 null。
type bangumiEpisode struct {
	SeasonID      *string    `json:"seasonId"`
	EpisodeID     int64      `json:"episodeId"`
	EpisodeTitle  string     `json:"episodeTitle"`
	EpisodeNumber string     `json:"episodeNumber"`
	LastWatched   *time.Time `json:"lastWatched"`
	AirDate       *time.Time `json:"airDate"`
}

// Bangumi GET bangumi/:bangumiId
// 作品详情：bangumiId 按季 ID 解析（search/anime 给出的 bangumiId、animeId 都可以），返回这一季和它的全部集。
// 不是整数或季不存在时，按协议的"资源未找到"返回：HTTP 200、success 为 false、bangumi 为 null。
func (h *Handler) Bangumi(c *echo.Context) error {
	start := time.Now()
	notFound := bangumiResponse{responseBase: animeNotFound}
	id, err := strconv.ParseInt(c.Param("bangumiId"), 10, 64)
	if err != nil {
		logRequest(c, start, "dandanplay bangumi", slog.String("bangumi_id", c.Param("bangumiId")), slog.Bool("found", false))
		return c.JSON(http.StatusOK, notFound)
	}

	season, found, err := h.svc.Season(c.Request().Context(), id)
	if err != nil {
		return serverError(c, err, bangumiResponse{responseBase: failed})
	}
	logRequest(c, start, "dandanplay bangumi", slog.Int64("bangumi_id", id), slog.Bool("found", found), slog.Int("episodes", len(season.Episodes)))
	if !found {
		return c.JSON(http.StatusOK, notFound)
	}
	return c.JSON(http.StatusOK, bangumiResponse{responseBase: succeeded, Bangumi: toBangumi(season)})
}

// toBangumi 一季转成作品详情。集的顺序和标题与 search/episodes 相同，episodeNumber 是集号；
// 目录里没有的信息输出零值（见 bangumiDetails）。
func toBangumi(s Season) *bangumiDetails {
	typ, description := typeOf(s)
	b := &bangumiDetails{
		AnimeID:         s.ID,
		BangumiID:       strconv.FormatInt(s.ID, 10),
		AnimeTitle:      s.Name,
		Type:            typ,
		TypeDescription: description,
		Titles:          []struct{}{},
		Seasons:         []struct{}{},
		Episodes:        make([]bangumiEpisode, len(s.Episodes)),
		Metadata:        []string{},
		RatingDetails:   map[string]float64{},
		Relateds:        []struct{}{},
		Similars:        []struct{}{},
		Tags:            []struct{}{},
		OnlineDatabases: []struct{}{},
		Trailers:        []struct{}{},
	}
	for i, e := range s.Episodes {
		b.Episodes[i] = bangumiEpisode{EpisodeID: e.ID, EpisodeTitle: episodeTitle(e), EpisodeNumber: strconv.Itoa(e.Number)}
	}
	return b
}

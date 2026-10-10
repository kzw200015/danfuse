package dandan

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

// matchRequest 官方 MatchRequest 里用到的字段。fileHash、fileSize、videoDuration 不用：目录里没有文件的 hash，只按文件名识别。
type matchRequest struct {
	FileName  string `json:"fileName"`
	MatchMode string `json:"matchMode"`
}

// matchResponse 官方的 MatchResponseV2。
type matchResponse struct {
	responseBase
	IsMatched bool          `json:"isMatched"`
	Matches   []matchResult `json:"matches"`
}

// matchResult 官方的 MatchResultV2：一个候选，直接到集。偏移已经在 comment 合并时按绑定校正过，shift 为 0；
// 海报不经弹弹 API 提供，imageUrl 为 null。
type matchResult struct {
	EpisodeID       int64   `json:"episodeId"`
	AnimeID         int64   `json:"animeId"`
	AnimeTitle      string  `json:"animeTitle"`
	EpisodeTitle    string  `json:"episodeTitle"`
	Type            string  `json:"type"`
	TypeDescription string  `json:"typeDescription"`
	Shift           float64 `json:"shift"`
	ImageURL        *string `json:"imageUrl"`
}

// Match POST match
// 只按文件名识别，交给 Service.Match：候选按可能性排序，确定时 isMatched 为 true，客户端直接采用；
// 否则由用户在候选里选。matchMode 为 hashOnly（目录里没有文件的 hash）、请求体不是 JSON 时没有匹配结果，
// 客户端改让用户手动搜索。
//
// 日志记下文件名、认出的标题、季号、集号（没有 episode 字段即文件名里没认出集号，没有搜索）、候选数和是否确定，
// 确定时另记采用的季和集。
func (h *Handler) Match(c *echo.Context) error {
	start := time.Now()
	resp := matchResponse{responseBase: succeeded, Matches: []matchResult{}}
	var req matchRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil || req.MatchMode == "hashOnly" {
		logRequest(c, start, "dandanplay match", slog.String("file_name", req.FileName), slog.String("match_mode", req.MatchMode),
			slog.Bool("invalid_body", err != nil), slog.Int("candidates", 0), slog.Bool("matched", false))
		return c.JSON(http.StatusOK, resp)
	}

	result, err := h.svc.Match(c.Request().Context(), req.FileName, maxSeasons)
	if err != nil {
		return serverError(c, err, matchResponse{responseBase: failed, Matches: []matchResult{}})
	}
	resp.IsMatched = result.Exact
	for _, s := range result.Candidates {
		resp.Matches = append(resp.Matches, toMatchResult(s, s.Episodes[0])) // 每个候选恰好带着认出的那一集
	}
	attrs := []slog.Attr{
		slog.String("file_name", req.FileName),
		slog.String("title", result.Parsed.Title),
		optionalInt("season", result.Parsed.Season),
		optionalInt("episode", result.Parsed.Episode),
		slog.Int("candidates", len(result.Candidates)),
		slog.Bool("matched", result.Exact),
	}
	if result.Exact {
		attrs = append(attrs, slog.Int64("anime_id", resp.Matches[0].AnimeID), slog.Int64("episode_id", resp.Matches[0].EpisodeID))
	}
	logRequest(c, start, "dandanplay match", attrs...)
	return c.JSON(http.StatusOK, resp)
}

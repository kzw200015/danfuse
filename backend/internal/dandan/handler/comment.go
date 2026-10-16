package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// commentResponse 官方的 CommentResponseV2：只有 count 和 comments，不带 ResponseBase。
type commentResponse struct {
	Count    int       `json:"count"`
	Comments []comment `json:"comments"` // 插件读不到这个字段时按出错处理，任何时候都输出 []
}

// comment 官方的 CommentData。插件只读 p 和 m。
type comment struct {
	CID int64  `json:"cid"`
	P   string `json:"p"`
	M   string `json:"m"`
}

// Comment GET comment/:episodeId
// 这一集所有绑定合并后的全部弹幕原文，按校正后时间升序。withRelated、from、chConvert 忽略。
// 不存在的 episodeId（包括不是整数的）、没有绑定的集返回空列表。
func (h *Handler) Comment(c *echo.Context) error {
	start := time.Now()
	resp := commentResponse{Comments: []comment{}}
	episodeID, err := strconv.ParseInt(c.Param("episodeId"), 10, 64)
	if err != nil {
		logRequest(c, start, "dandanplay comment", slog.String("episode_id", c.Param("episodeId")), slog.Int("count", 0))
		return c.JSON(http.StatusOK, resp)
	}

	items, err := h.svc.Comments(c.Request().Context(), episodeID)
	if err != nil {
		return serverError(c, err, resp) // 官方没有为 comment 定义错误响应，响应体沿用它的 schema
	}
	resp.Count = len(items)
	resp.Comments = make([]comment, len(items))
	for i, it := range items {
		resp.Comments[i] = toComment(it)
	}
	logRequest(c, start, "dandanplay comment", slog.Int64("episode_id", episodeID), slog.Int("count", len(items)))
	return c.JSON(http.StatusOK, resp)
}

// userPrefixes p 里用户字段的平台前缀，插件靠它按来源过滤（[BiliBili] 归为 B 站）。前缀是协议对平台的叫法，
// 所以放在弹弹 API 一侧；没有平台的弹幕源不加前缀，插件把它归为"弹弹"。
var userPrefixes = map[danmaku.Platform]string{
	danmaku.PlatformBilibili: "[BiliBili]",
}

// toComment 一条弹幕转成协议里的 comment：cid 由平台与原始 ID 现算；
// p 为"秒（两位小数）,模式,颜色（十进制）,用户"，用户字段只写平台前缀，后面不跟任何内容。
// 协议只定义了模式 1、4、5，逆向（6）输出为 1。
func toComment(it danmaku.Item) comment {
	mode := it.Mode
	if mode == danmaku.ModeReverse {
		mode = danmaku.ModeScroll
	}
	return comment{
		CID: danmaku.CID(it.Platform, it.SourceID),
		P:   fmt.Sprintf("%.2f,%d,%d,%s", float64(it.TimeMs)/1000, mode, it.Color, userPrefixes[it.Platform]),
		M:   it.Text,
	}
}

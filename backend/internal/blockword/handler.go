package blockword

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
)

// Handler 屏蔽词的管理 API，路由注册在 server/router.go。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// List GET /api/blocked-words
// 全部屏蔽词，新加的在前。
func (h *Handler) List(c *echo.Context) error {
	words, err := h.svc.List(c.Request().Context())
	if err != nil {
		return err
	}
	return response.OK(c, words)
}

type createBlockedWordRequest struct {
	Kind    string `json:"kind"` // keyword 关键词、regex 正则
	Pattern string `json:"pattern"`

	word danmaku.BlockedWord
}

func (r *createBlockedWordRequest) Validate() error {
	var err error
	r.word, err = danmaku.ParseBlockedWord(danmaku.BlockedWordKind(r.Kind), r.Pattern)
	if err != nil {
		return request.InvalidParam(err.Error())
	}
	return nil
}

// Create POST /api/blocked-words {kind, pattern}
// 新增一条屏蔽词，返回 201 和这条屏蔽词；已有相同的时 409。
func (h *Handler) Create(c *echo.Context) error {
	req, err := request.Bind[createBlockedWordRequest](c)
	if err != nil {
		return err
	}
	word, err := h.svc.Create(c.Request().Context(), req.word)
	if err != nil {
		return err
	}
	return response.Created(c, word)
}

type blockedWordRequest struct {
	ID int64 `param:"id"`
}

func (r *blockedWordRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("屏蔽词 ID 不合法")
	}
	return nil
}

// Delete DELETE /api/blocked-words/:id
func (h *Handler) Delete(c *echo.Context) error {
	req, err := request.Bind[blockedWordRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// relatedResponse related 的响应：ResponseBase 加 relateds。
type relatedResponse struct {
	responseBase
	Relateds []struct{} `json:"relateds"`
}

// Related GET related/:episodeId
// 官方已下线这个接口，插件仍会在每次取弹幕后调用。固定返回空的 relateds，插件也就不会再去调 extcomment。
func (h *Handler) Related(c *echo.Context) error {
	return c.JSON(http.StatusOK, relatedResponse{responseBase: succeeded, Relateds: []struct{}{}})
}

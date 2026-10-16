package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

type imageRequest struct {
	ID int64 `param:"id"`
}

func (r *imageRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("图片 ID 不合法")
	}
	return nil
}

// GetImage GET /api/images/:id
// 统一响应约定的例外：成功时直接返回原始字节和 content-type；出错时照常 return error，由全局 errorHandler 输出统一结构。
// 海报内容变了会换成新的图片 ID，同一个 ID 的内容不会变，所以可以永久缓存。
func (h *Handler) GetImage(c *echo.Context) error {
	req, err := request.Bind[imageRequest](c)
	if err != nil {
		return err
	}
	img, err := h.svc.GetImage(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, img.ContentType, img.Data)
}

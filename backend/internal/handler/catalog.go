package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type CatalogHandler struct {
	svc *service.CatalogService
}

func NewCatalogHandler(svc *service.CatalogService) *CatalogHandler {
	return &CatalogHandler{svc: svc}
}

// ListSeries GET /api/series
// 全部剧，不分页，每项带海报的图片 ID、季数、集数与绑定统计。
func (h *CatalogHandler) ListSeries(c *echo.Context) error {
	series, err := h.svc.ListSeries(c.Request().Context())
	if err != nil {
		return err
	}
	return response.OK(c, series)
}

type seriesRequest struct {
	ID int64 `param:"id"`
}

func (r *seriesRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("剧 ID 不合法")
	}
	return nil
}

// GetSeries GET /api/series/:id
// 一部剧的完整子树：季 → 季绑定、集 → 绑定。
func (h *CatalogHandler) GetSeries(c *echo.Context) error {
	req, err := bind[seriesRequest](c)
	if err != nil {
		return err
	}
	series, err := h.svc.GetSeries(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, series)
}

// DeleteSeries DELETE /api/series/:id
// 它的季、集、季绑定、绑定、弹幕和海报一起删除。
func (h *CatalogHandler) DeleteSeries(c *echo.Context) error {
	req, err := bind[seriesRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteSeries(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

type seasonRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("季 ID 不合法")
	}
	return nil
}

// DeleteSeason DELETE /api/seasons/:id
// 它的集、季绑定、绑定和弹幕一起删除。
func (h *CatalogHandler) DeleteSeason(c *echo.Context) error {
	req, err := bind[seasonRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteSeason(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

type episodeRequest struct {
	ID int64 `param:"id"`
}

func (r *episodeRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("集 ID 不合法")
	}
	return nil
}

// DeleteEpisode DELETE /api/episodes/:id
// 它的绑定和弹幕一起删除。
func (h *CatalogHandler) DeleteEpisode(c *echo.Context) error {
	req, err := bind[episodeRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteEpisode(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

type imageRequest struct {
	ID int64 `param:"id"`
}

func (r *imageRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("图片 ID 不合法")
	}
	return nil
}

// GetImage GET /api/images/:id
// 统一响应约定的例外：成功时直接返回原始字节和 content-type；出错时照常 return error，由全局 errorHandler 输出统一结构。
// 海报内容变了会换成新的图片 ID，同一个 ID 的内容不会变，所以可以永久缓存。
func (h *CatalogHandler) GetImage(c *echo.Context) error {
	req, err := bind[imageRequest](c)
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

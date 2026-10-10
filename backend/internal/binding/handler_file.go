package binding

import (
	"path"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
)

// readUploadedFiles 单集上传：读出字段 files 的全部文件，名称为上传的文件名（去掉目录），超出上限（config.DanmakuFile 里单集的那组）时返回 400。
// 在 request.Bind 之前调用。
func (h *Handler) readUploadedFiles(c *echo.Context) ([]UploadedFile, error) {
	limits := request.FileLimits{MaxFiles: h.upload.MaxFiles, MaxFileMB: h.upload.MaxFileMB, MaxUploadMB: h.upload.MaxUploadMB}
	read, err := request.ReadFiles(c, limits)
	if err != nil {
		return nil, err
	}
	files := make([]UploadedFile, len(read))
	for i, f := range read {
		files[i] = UploadedFile{Name: path.Base(f.Name), Data: f.Data}
	}
	return files, nil
}

// CreateFromFiles POST /api/episodes/:id/file-bindings（multipart，字段 files 可以有多份）
// 一次上传的几份弹幕文件合起来是一个弹幕源，当场解析、写入，返回 201 和绑定；有一份认不出就不创建（422）。
func (h *Handler) CreateFromFiles(c *echo.Context) error {
	files, err := h.readUploadedFiles(c)
	if err != nil {
		return err
	}
	req, err := request.Bind[episodeRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.CreateFromFiles(c.Request().Context(), req.ID, files)
	if err != nil {
		return err
	}
	return response.Created(c, binding)
}

// AppendFiles POST /api/bindings/:id/files（multipart，字段 files 可以有多份）
// 追加文件，返回绑定、新加入与跳过的份数和新增条数；有一份认不出就什么都不加（422）。
func (h *Handler) AppendFiles(c *echo.Context) error {
	files, err := h.readUploadedFiles(c)
	if err != nil {
		return err
	}
	req, err := request.Bind[bindingRequest](c)
	if err != nil {
		return err
	}
	result, err := h.svc.AppendFiles(c.Request().Context(), req.ID, files)
	if err != nil {
		return err
	}
	return response.OK(c, result)
}

// Reparse POST /api/bindings/:id/reparse
// 按保存的弹幕文件重新解析，替换现有弹幕，返回绑定。
func (h *Handler) Reparse(c *echo.Context) error {
	req, err := request.Bind[bindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.Reparse(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, binding)
}

// ListFiles GET /api/bindings/:id/files
// 绑定里的弹幕文件（文件名、大小、加入时间），不含内容。
func (h *Handler) ListFiles(c *echo.Context) error {
	req, err := request.Bind[bindingRequest](c)
	if err != nil {
		return err
	}
	files, err := h.svc.ListFiles(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, files)
}

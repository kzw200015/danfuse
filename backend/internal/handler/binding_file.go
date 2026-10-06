package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

// 一次上传弹幕文件的上限是配置项（config.DanmakuFile）；一个绑定累计追加的文件不设上限。
const (
	// multipartOverhead 请求体在文件之外的余量：multipart 的分隔符和每份文件的头部
	multipartOverhead = 1 << 20
	// multipartMemory 解析 multipart 时留在内存里的上限，超出的文件内容先写到临时文件；
	// 文件随后整份读进内存交给 service，不必在表单的缓冲里再留一份
	multipartMemory = 1 << 20
)

// readUploadedFiles 读出 multipart 请求里字段 files 的全部文件，超出上限时返回 400。
// 在 bind 之前调用：先给请求体套上大小限制再解析，bind 用的是解析好的表单。
func readUploadedFiles(c *echo.Context, limits config.DanmakuFile) ([]service.UploadedFile, error) {
	errUploadTooLarge := invalidParam(fmt.Sprintf("一次上传的文件合计不能超过 %d MB", limits.MaxUploadMB))
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limits.MaxUploadMB<<20+multipartOverhead)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, errUploadTooLarge
		}
		return nil, apierr.ErrBadRequest.Wrap(err)
	}
	headers := r.MultipartForm.File["files"]
	switch {
	case len(headers) == 0:
		return nil, invalidParam("请选择弹幕文件")
	case len(headers) > limits.MaxFiles:
		return nil, invalidParam(fmt.Sprintf("一次最多上传 %d 份文件", limits.MaxFiles))
	}
	var total int64
	files := make([]service.UploadedFile, len(headers))
	for i, h := range headers {
		if h.Size > limits.MaxFileMB<<20 {
			return nil, invalidParam(fmt.Sprintf("「%s」超过 %d MB", h.Filename, limits.MaxFileMB))
		}
		if total += h.Size; total > limits.MaxUploadMB<<20 {
			return nil, errUploadTooLarge
		}
		f, err := h.Open()
		if err != nil {
			return nil, fmt.Errorf("open uploaded file %q: %w", h.Filename, err)
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("read uploaded file %q: %w", h.Filename, err)
		}
		files[i] = service.UploadedFile{Name: h.Filename, Data: data}
	}
	return files, nil
}

// CreateFromFiles POST /api/episodes/:id/file-bindings（multipart，字段 files 可以有多份）
// 一次上传的几份弹幕文件合起来是一个弹幕源，当场解析、写入，返回 201 和绑定；有一份认不出就不创建（422）。
func (h *BindingHandler) CreateFromFiles(c *echo.Context) error {
	files, err := readUploadedFiles(c, h.upload)
	if err != nil {
		return err
	}
	req, err := bind[episodeRequest](c)
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
func (h *BindingHandler) AppendFiles(c *echo.Context) error {
	files, err := readUploadedFiles(c, h.upload)
	if err != nil {
		return err
	}
	req, err := bind[bindingRequest](c)
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
func (h *BindingHandler) Reparse(c *echo.Context) error {
	req, err := bind[bindingRequest](c)
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
func (h *BindingHandler) ListFiles(c *echo.Context) error {
	req, err := bind[bindingRequest](c)
	if err != nil {
		return err
	}
	files, err := h.svc.ListFiles(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, files)
}

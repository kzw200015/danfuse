package binding

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
)

// 一次上传弹幕文件的上限是配置项（config.DanmakuFile）；一个绑定累计追加的文件不设上限。
const (
	// multipartOverhead 请求体在文件之外的余量：multipart 的分隔符和每份文件的头部
	multipartOverhead = 1 << 20
	// multipartMemory 解析 multipart 时留在内存里的上限，超出的文件内容先写到临时文件；
	// 文件随后整份读进内存交给 service，不必在表单的缓冲里再留一份
	multipartMemory = 1 << 20
)

// uploadLimits 一次上传的上限：份数、单份大小和合计大小。单集上传与按季上传的份数、合计上限是不同的配置项。
type uploadLimits struct {
	maxFiles    int
	maxFileMB   int64
	maxUploadMB int64
}

// errUploadTooLarge 合计大小超限：请求体超过上限，或文件大小加起来超过上限。
func (l uploadLimits) errUploadTooLarge() error {
	return request.InvalidParam(fmt.Sprintf("一次上传的文件合计不能超过 %d MB", l.maxUploadMB))
}

// upload 一份待读出的上传文件。
type upload struct {
	name   string // 超限提示里和读出的 UploadedFile 里的名称，默认为上传的文件名
	header *multipart.FileHeader
}

// parseUpload 给请求体套上大小限制再解析 multipart，返回字段 files 的全部文件；没有文件或份数超限时返回 400。
// 在 request.Bind 之前调用：Bind 用的是解析好的表单。
func parseUpload(c *echo.Context, limits uploadLimits) ([]upload, error) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limits.maxUploadMB<<20+multipartOverhead)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, limits.errUploadTooLarge()
		}
		return nil, apierr.ErrBadRequest.Wrap(err)
	}
	headers := r.MultipartForm.File["files"]
	switch {
	case len(headers) == 0:
		return nil, request.InvalidParam("请选择弹幕文件")
	case len(headers) > limits.maxFiles:
		return nil, request.InvalidParam(fmt.Sprintf("一次最多上传 %d 份文件", limits.maxFiles))
	}
	uploads := make([]upload, len(headers))
	for i, fh := range headers {
		uploads[i] = upload{name: fh.Filename, header: fh}
	}
	return uploads, nil
}

// readUploads 读出每份文件的内容，单份或合计超限时返回 400。
func readUploads(uploads []upload, limits uploadLimits) ([]UploadedFile, error) {
	var total int64
	files := make([]UploadedFile, len(uploads))
	for i, u := range uploads {
		if u.header.Size > limits.maxFileMB<<20 {
			return nil, request.InvalidParam(fmt.Sprintf("「%s」超过 %d MB", u.name, limits.maxFileMB))
		}
		if total += u.header.Size; total > limits.maxUploadMB<<20 {
			return nil, limits.errUploadTooLarge()
		}
		f, err := u.header.Open()
		if err != nil {
			return nil, fmt.Errorf("open uploaded file %q: %w", u.name, err)
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("read uploaded file %q: %w", u.name, err)
		}
		files[i] = UploadedFile{Name: u.name, Data: data}
	}
	return files, nil
}

// readUploadedFiles 单集上传：读出字段 files 的全部文件，名称为上传的文件名，超出上限时返回 400。
// 在 request.Bind 之前调用。
func (h *Handler) readUploadedFiles(c *echo.Context) ([]UploadedFile, error) {
	limits := uploadLimits{maxFiles: h.upload.MaxFiles, maxFileMB: h.upload.MaxFileMB, maxUploadMB: h.upload.MaxUploadMB}
	uploads, err := parseUpload(c, limits)
	if err != nil {
		return nil, err
	}
	return readUploads(uploads, limits)
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

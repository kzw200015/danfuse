package request

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

// maxFieldBytes 文件以外的字段合计的上限，也是请求体在文件之外的余量（multipart 的分隔符、每个 part 的头部和这些字段）。
const maxFieldBytes = 1 << 20

// FileLimits 一次上传的上限：份数、单份大小和合计大小。
type FileLimits struct {
	MaxFiles    int
	MaxFileMB   int64
	MaxUploadMB int64
}

// errTooLarge 合计大小超限：请求体超过上限，或文件大小加起来超过上限。
func (l FileLimits) errTooLarge() error {
	return InvalidParam(fmt.Sprintf("一次上传的文件合计不能超过 %d MB", l.MaxUploadMB))
}

// File 上传的一份文件。
type File struct {
	Name string // 请求里原样的文件名，不去掉目录（按季上传用它传相对路径）
	Data []byte
}

// ReadFiles 流式读取 multipart 请求体，边读边查份数、单份和合计大小，返回字段 files 的全部文件（按请求里的顺序）；
// 没有文件或超出上限时返回 400，提示里的文件名取请求里原样的文件名。不经过临时文件，也没有 part 数的上限。
// 其他字段放回 Request.Form，随后照常 Bind；所以在 Bind 之前调用。
func ReadFiles(c *echo.Context, limits FileLimits) ([]File, error) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limits.MaxUploadMB<<20+maxFieldBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, apierr.ErrBadRequest.Wrap(err)
	}
	var (
		files  []File
		fields = make(url.Values)
		total  int64                 // 已读的文件合计
		rest   int64 = maxFieldBytes // 字段还能用的字节数
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, readError(err, limits)
		}
		name, isFile := fileName(part)
		switch {
		case part.FormName() == "":
			// 与 ParseMultipartForm 一样跳过没有字段名的 part
		case !isFile:
			value, err := readAtMost(part, rest)
			if err != nil {
				return nil, readError(err, limits)
			}
			if rest -= int64(len(value)); rest < 0 {
				return nil, InvalidParam("请求里文件以外的字段太大")
			}
			fields.Add(part.FormName(), string(value))
		case part.FormName() != "files":
			// 别的字段里的文件不读，NextPart 会跳过它的内容
		case len(files) == limits.MaxFiles:
			return nil, InvalidParam(fmt.Sprintf("一次最多上传 %d 份文件", limits.MaxFiles))
		default:
			data, err := readAtMost(part, limits.MaxFileMB<<20)
			if err != nil {
				return nil, readError(err, limits)
			}
			if int64(len(data)) > limits.MaxFileMB<<20 {
				return nil, InvalidParam(fmt.Sprintf("「%s」超过 %d MB", name, limits.MaxFileMB))
			}
			if total += int64(len(data)); total > limits.MaxUploadMB<<20 {
				return nil, limits.errTooLarge()
			}
			files = append(files, File{Name: name, Data: data})
		}
	}
	if len(files) == 0 {
		return nil, InvalidParam("请选择要上传的文件")
	}
	// 与 ParseMultipartForm 的结果一样：Form 含查询参数和表单字段，之后的 ParseMultipartForm 直接返回
	r.MultipartForm = &multipart.Form{Value: fields}
	r.PostForm = fields
	r.Form = r.URL.Query()
	for k, v := range fields {
		r.Form[k] = append(r.Form[k], v...)
	}
	return files, nil
}

// fileName part 的文件名，不像 Part.FileName 那样去掉目录；与 ParseMultipartForm 一样，文件名为空的 part 是普通字段。
func fileName(part *multipart.Part) (string, bool) {
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return "", false
	}
	name := params["filename"]
	return name, name != ""
}

// readAtMost 读出 part 的内容，最多读 limit+1 个字节：读满 limit+1 说明超限，调用方按长度判断。
func readAtMost(part *multipart.Part, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(part, limit+1))
}

// readError 读请求体出错：请求体超过上限为合计超限，其他（多半是格式不对或连接断开）为 400。
func readError(err error, limits FileLimits) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return limits.errTooLarge()
	}
	return apierr.ErrBadRequest.Wrap(err)
}

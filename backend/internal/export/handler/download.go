package handler

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// download 一次导出的下载。open 交给 service（service.Open），在导出确定能进行时输出响应头；
// finish 处理 service 返回的错误，按响应头输出了没有分两种。
type download struct {
	c           *echo.Context
	contentType string
	started     bool // 响应头已经输出
}

// open 输出 200 与响应头（Content-Type、Content-Disposition），返回写响应体的 io.Writer。
// 显式提交响应头：内容可能先进了编码器的缓冲区，不等第一次写出，之后出的错一律按"已经输出"处理。
func (d *download) open(filename string) io.Writer {
	resp := d.c.Response()
	resp.Header().Set(echo.HeaderContentType, d.contentType)
	resp.Header().Set(echo.HeaderContentDisposition, contentDisposition(filename))
	resp.WriteHeader(http.StatusOK)
	d.started = true
	return resp
}

// finish 响应头还没输出时原样返回 err，由全局 errorHandler 输出统一的错误响应。
// 已经输出时响应改不成错误了（errorHandler 遇到已提交的响应什么也不做）：记 5xx 日志，再中止连接（http.ErrAbortHandler，
// Recover 中间件会把它原样抛给 net/http），让浏览器把下载标为失败，而不是存下一份看着正常、实际不完整的文件。
func (d *download) finish(err error) error {
	if err == nil || !d.started {
		return err
	}
	response.LogServerError(d.c, err)
	panic(http.ErrAbortHandler)
}

// contentDisposition 下载的 Content-Disposition：attachment，同时给出 ASCII 的 filename 兜底（只认 filename 的客户端用）
// 与 RFC 5987 的 filename*（UTF-8，浏览器优先用它，中文文件名不会变成乱码）。
func contentDisposition(filename string) string {
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiFallback(filename), percentEncode(filename))
}

// asciiFallback 可打印的 ASCII 字符之外的字符，以及 quoted-string 里要转义的 " 与 \，都换成 _。
func asciiFallback(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r > '~' || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, s)
}

// percentEncode RFC 5987 的 value-chars：attr-char 原样写出，其余字节按 %XX 编码。
// 不用 url.PathEscape：它保留 ' ( ) 等不属于 attr-char 的字符，' 正是 filename* 里的分隔符。
func percentEncode(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if isAttrChar(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// isAttrChar RFC 5987 的 attr-char：字母、数字与 ! # $ & + - . ^ _ ` | ~。
func isAttrChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

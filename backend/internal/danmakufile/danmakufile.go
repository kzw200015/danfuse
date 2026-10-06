// Package danmakufile 弹幕文件：用户上传的、保存着一批弹幕的文件（见 GLOSSARY.md）。只做解析，纯计算，不访问数据库。
// 弹幕文件不是源适配器：没有链接、不能重新拉取，弹幕不属于任何平台（见 docs/adr/0004）。目前只认 B 站 XML。
package danmakufile

import (
	"fmt"
	"path"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
)

// Error 一份弹幕文件解析不了。Message 是给用户看的提示，带着文件名；Err 是底层原因，只进日志。
// 调用方用 errors.AsType 取出。
type Error struct {
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Parse 解析一份弹幕文件，name 只用于提示。原始弹幕 ID 直接取 XML 里的 dmid：同一个视频不同日期的快照
// 按 ID 去重，重复加入同一份文件也不会多出弹幕。认不出、或文件不完整时返回 *Error，不返回部分弹幕；
// 认得出但没有弹幕的文件解出 0 条。
func Parse(name string, data []byte) ([]danmaku.Danmaku, error) {
	items, err := bilifmt.Decode(data)
	if err != nil {
		return nil, &Error{Message: fmt.Sprintf("无法识别「%s」：目前只支持 B 站的 XML 弹幕文件，且文件要完整", name), Err: err}
	}
	return items, nil
}

// Title 用弹幕文件建绑定时的标题：文件名去掉扩展名；去掉之后为空时用原文件名。
func Title(name string) string {
	if t := strings.TrimSpace(strings.TrimSuffix(name, path.Ext(name))); t != "" {
		return t
	}
	return name
}

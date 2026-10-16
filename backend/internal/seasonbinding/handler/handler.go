// Package handler 季绑定的管理 API：只绑定与校验参数（表单、上传的文件夹）、调用 service、输出响应。
package handler

import (
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Handler 季绑定的管理 API，路由见同包的 routes.go。这里只有 Handler 与两种季绑定共用的表单、请求结构；
// 接口按功能分文件，与 service 里的同名：collection.go、folder.go、detail.go、follow.go、backfill.go、delete.go、episode_rule.go。
type Handler struct {
	svc    *service.Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限，上传文件夹用其中按季的那组
}

func NewHandler(svc *service.Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

const invalidMapping = "集号对应必须是不小于 0 的整数"

// ruleForm 两种季绑定的表单（和预览）共有的部分：集号规则与集号对应。嵌进各自的请求结构体，在 Validate 里调用 parse。
type ruleForm struct {
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则
	MappingFrom     *int32   `json:"mappingFrom"`
	MappingTo       *int32   `json:"mappingTo"`

	rule    source.EpisodeRule
	mapping source.Mapping
}

// parse 校验并解析集号规则与集号对应：对应没传或小于 0 时 400，规则不合法时 400（提示由 source.ParseEpisodeRule 给出）。
func (f *ruleForm) parse() error {
	if !validMapping(f.MappingFrom) || !validMapping(f.MappingTo) {
		return request.InvalidParam(invalidMapping)
	}
	f.mapping = source.Mapping{From: int(*f.MappingFrom), To: int(*f.MappingTo)}
	rule, err := source.ParseEpisodeRule(f.EpisodePatterns)
	if err != nil {
		return request.InvalidParam(err.Error())
	}
	f.rule = rule
	return nil
}

// validMapping 集号对应的一端：没传或小于 0 时不合法。
func validMapping(v *int32) bool {
	return v != nil && *v >= 0
}

// parseLink 去掉贴的链接前后的空白，为空时为 400。
func parseLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", request.InvalidParam("请粘贴合集的链接")
	}
	return link, nil
}

type seasonBindingRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	return nil
}

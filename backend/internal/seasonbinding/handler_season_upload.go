package seasonbinding

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

type previewSeasonUploadRequest struct {
	SeasonID        int64    `param:"id"`
	Labels          []string `json:"labels"`          // 条目名称，"所选文件夹名 / 子目录名或文件名去掉扩展名"
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则

	rule source.EpisodeRule
}

func (r *previewSeasonUploadRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	if len(r.Labels) == 0 {
		return request.InvalidParam("请选择弹幕文件夹")
	}
	var err error
	r.rule, err = parseRule(r.EpisodePatterns)
	return err
}

// PreviewSeasonUpload POST /api/seasons/:id/file-bindings/preview {labels, episodePatterns}
// 按季上传的预览：按集号规则从条目名称认出序号，返回 {items: [{label, number, reason}]}，顺序与 labels 相同。
// 不上传文件、不保存任何东西。
func (h *Handler) PreviewSeasonUpload(c *echo.Context) error {
	req, err := request.Bind[previewSeasonUploadRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewSeasonUpload(c.Request().Context(), req.SeasonID, req.Labels, req.rule)
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

// createSeasonUploadRequest 按季上传的 multipart 里除文件以外的字段。files 的文件名是以所选文件夹名开头的相对路径。
type createSeasonUploadRequest struct {
	SeasonID int64  `param:"id"`
	Targets  string `form:"targets"` // JSON 数组，每个条目一项 {label, episodeId}，episodeId 取自预览

	targets []seasonUploadTarget
}

// seasonUploadTarget 一个条目对到的集。
type seasonUploadTarget struct {
	Label     string `json:"label"`
	EpisodeID int64  `json:"episodeId"`
}

func (r *createSeasonUploadRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	if err := json.Unmarshal([]byte(r.Targets), &r.targets); err != nil {
		return apierr.ErrBadRequest.Wrap(fmt.Errorf("decode targets: %w", err))
	}
	episodes := make(map[int64]string, len(r.targets)) // 目标集 → 对到它的条目
	for _, t := range r.targets {
		if other, ok := episodes[t.EpisodeID]; ok {
			return request.InvalidParam(fmt.Sprintf("「%s」和「%s」对到了同一集，请重新预览", other, t.Label))
		}
		episodes[t.EpisodeID] = t.Label
	}
	return nil
}

// seasonGroup 按路径分出的一个条目，还没有目标集。
type seasonGroup struct {
	label string
	dir   bool // 子目录合成的条目；false 为顶层的一份文件
	files []binding.UploadedFile
}

// groupSeasonFiles 按文件名（相对路径）把文件分成条目，按第一次出现的顺序返回，连同条目名称 → 下标。
// 每个路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"，扩展名不分大小写，文件夹名都相同；
// 子目录里的文件合成一个条目"文件夹名 / 子目录名"，顶层的文件各自一个条目"文件夹名 / 文件名去掉扩展名"，条目名称不能重复。
func groupSeasonFiles(files []request.File) ([]seasonGroup, map[string]int, error) {
	var (
		groups []seasonGroup
		folder string
	)
	index := make(map[string]int)
	for i, f := range files {
		p := f.Name
		parts := strings.Split(p, "/")
		if len(parts) > 3 {
			return nil, nil, request.InvalidParam("目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：" + p)
		}
		if len(parts) < 2 || slices.Contains(parts, "") {
			return nil, nil, request.InvalidParam("文件路径不合法：" + p)
		}
		if !strings.HasSuffix(strings.ToLower(p), ".xml") {
			return nil, nil, request.InvalidParam("只能上传 XML 弹幕文件：" + p)
		}
		if i == 0 {
			folder = parts[0]
		}
		if parts[0] != folder {
			return nil, nil, request.InvalidParam("文件要在同一个文件夹里：" + p)
		}
		dir := len(parts) == 3
		name := parts[1]
		if !dir {
			// 与前端一样去掉最后一个点及其后面的部分；文件名只有扩展名时保留原样
			if dot := strings.LastIndexByte(name, '.'); dot > 0 {
				name = name[:dot]
			}
		}
		label := parts[0] + source.LabelSeparator + name
		g, ok := index[label]
		switch {
		case !ok:
			g = len(groups)
			index[label] = g
			groups = append(groups, seasonGroup{label: label, dir: dir})
		case !dir || !groups[g].dir:
			return nil, nil, request.InvalidParam("条目名称重复：" + label)
		}
		groups[g].files = append(groups[g].files, binding.UploadedFile(f))
	}
	return groups, index, nil
}

// seasonEntries 把文件分成条目、配上目标集，按 targets 的顺序返回。
// 条目与目标一一对应：每个目标都有文件，每个条目恰好一个目标。
func seasonEntries(files []request.File, targets []seasonUploadTarget) ([]binding.SeasonEntry, error) {
	groups, index, err := groupSeasonFiles(files)
	if err != nil {
		return nil, err
	}
	taken := make([]bool, len(groups))
	entries := make([]binding.SeasonEntry, len(targets))
	for i, t := range targets {
		g, ok := index[t.Label]
		switch {
		case !ok:
			return nil, request.InvalidParam(fmt.Sprintf("「%s」没有对应的文件", t.Label))
		case taken[g]:
			return nil, request.InvalidParam(fmt.Sprintf("「%s」对应了多个目标集", t.Label))
		}
		taken[g] = true
		entries[i] = binding.SeasonEntry{Label: t.Label, EpisodeID: t.EpisodeID, Files: groups[g].files}
	}
	if g := slices.Index(taken, false); g >= 0 {
		return nil, request.InvalidParam(fmt.Sprintf("「%s」没有指定目标集", groups[g].label))
	}
	return entries, nil
}

// CreateFromSeasonFiles POST /api/seasons/:id/file-bindings（multipart：files 可以有多份，文件名是相对路径；targets 是 JSON 数组）
// 按季上传：按文件名把文件分成条目，每个条目在 targets 指定的集上建一个用弹幕文件建的绑定，
// 留下一个文件夹的季绑定，返回 201 和 {bindings, added}。
// 上限（config.DanmakuFile 里按季的那组）、路径结构、条目与目标的对应有一项不满足就整次 400，什么都不保存。
func (h *Handler) CreateFromSeasonFiles(c *echo.Context) error {
	limits := request.FileLimits{MaxFiles: h.upload.SeasonMaxFiles, MaxFileMB: h.upload.MaxFileMB, MaxUploadMB: h.upload.SeasonMaxUploadMB}
	files, err := request.ReadFiles(c, limits)
	if err != nil {
		return err
	}
	req, err := request.Bind[createSeasonUploadRequest](c)
	if err != nil {
		return err
	}
	entries, err := seasonEntries(files, req.targets)
	if err != nil {
		return err
	}
	folder, _, _ := strings.Cut(files[0].Name, "/") // 分组时已确认路径都以同一个文件夹名开头
	created, err := h.svc.CreateFromSeasonFiles(c.Request().Context(), req.SeasonID, folder, entries)
	if err != nil {
		return err
	}
	return response.Created(c, created)
}

package binding

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// createSeasonUploadRequest 按季上传的 multipart 里除文件以外的字段。paths、targets 都是 JSON 数组，各占一个字段：
// 逐份一个字段时，500 份文件就会超过 Go 的 multipart 默认的 1000 个 part。
type createSeasonUploadRequest struct {
	SeasonID int64  `param:"id"`
	Paths    string `form:"paths"`   // 与 files 一一对应、顺序相同的相对路径，以所选文件夹名开头
	Targets  string `form:"targets"` // 每个条目一项 {label, episodeId}，episodeId 取自预览

	paths   []string
	entries []seasonUploadEntry // 按 targets 的顺序
}

// seasonUploadEntry 按路径分出的一个条目。
type seasonUploadEntry struct {
	label     string
	dir       bool  // 子目录合成的条目；false 为顶层的一份文件
	files     []int // 它的文件在 paths（也是 files）里的下标
	episodeID int64 // targets 给它指定的目标集
}

func (r *createSeasonUploadRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var targets []struct {
		Label     string `json:"label"`
		EpisodeID int64  `json:"episodeId"`
	}
	if err := json.Unmarshal([]byte(r.Paths), &r.paths); err != nil {
		return apierr.ErrBadRequest.Wrap(fmt.Errorf("decode paths: %w", err))
	}
	if err := json.Unmarshal([]byte(r.Targets), &targets); err != nil {
		return apierr.ErrBadRequest.Wrap(fmt.Errorf("decode targets: %w", err))
	}
	groups, err := groupSeasonPaths(r.paths)
	if err != nil {
		return err
	}
	// 条目与目标一一对应：每个目标都有文件，每个条目恰好一个目标
	index := make(map[string]int, len(groups))
	for i, g := range groups {
		index[g.label] = i
	}
	taken := make([]bool, len(groups))
	r.entries = make([]seasonUploadEntry, len(targets))
	for i, target := range targets {
		g, ok := index[target.Label]
		switch {
		case !ok:
			return request.InvalidParam(fmt.Sprintf("「%s」没有对应的文件", target.Label))
		case taken[g]:
			return request.InvalidParam(fmt.Sprintf("「%s」对应了多个目标集", target.Label))
		}
		taken[g] = true
		r.entries[i] = groups[g]
		r.entries[i].episodeID = target.EpisodeID
	}
	if g := slices.Index(taken, false); g >= 0 {
		return request.InvalidParam(fmt.Sprintf("「%s」没有指定目标集", groups[g].label))
	}
	return nil
}

// groupSeasonPaths 按相对路径把文件分成条目（还没有目标集），按第一次出现的顺序返回。
// 每个路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"，扩展名不分大小写，文件夹名都相同；
// 子目录里的文件合成一个条目"文件夹名 / 子目录名"，顶层的文件各自一个条目"文件夹名 / 文件名去掉扩展名"，条目名称不能重复。
func groupSeasonPaths(paths []string) ([]seasonUploadEntry, error) {
	var groups []seasonUploadEntry
	index := make(map[string]int)
	for i, p := range paths {
		parts := strings.Split(p, "/")
		if len(parts) > 3 {
			return nil, request.InvalidParam("目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：" + p)
		}
		if len(parts) < 2 || slices.Contains(parts, "") {
			return nil, request.InvalidParam("文件路径不合法：" + p)
		}
		if !strings.HasSuffix(strings.ToLower(p), ".xml") {
			return nil, request.InvalidParam("只能上传 XML 弹幕文件：" + p)
		}
		if folder, _, _ := strings.Cut(paths[0], "/"); parts[0] != folder {
			return nil, request.InvalidParam("文件要在同一个文件夹里：" + p)
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
			groups = append(groups, seasonUploadEntry{label: label, dir: dir})
		case !dir || !groups[g].dir:
			return nil, request.InvalidParam("条目名称重复：" + label)
		}
		groups[g].files = append(groups[g].files, i)
	}
	return groups, nil
}

// SeasonUpload 按季上传的请求：从 multipart 读出全部文件，按路径分好条目、配上目标集。
type SeasonUpload struct {
	SeasonID int64
	Folder   string // 所选的文件夹名，所有路径都以它开头
	Entries  []SeasonEntry
}

// ReadSeasonUpload 读出按季上传的请求（multipart：files 可以有多份，paths 与 targets 是 JSON 数组），供季绑定的 handler 使用：
// 按 paths 把文件分成条目，每个条目配上 targets 指定的集。上限（upload 里按季上传的那组）、路径结构、
// 条目与目标的对应有一项不满足就返回 400。
func ReadSeasonUpload(c *echo.Context, upload config.DanmakuFile) (SeasonUpload, error) {
	limits := uploadLimits{maxFiles: upload.SeasonMaxFiles, maxFileMB: upload.MaxFileMB, maxUploadMB: upload.SeasonMaxUploadMB}
	uploads, err := parseUpload(c, limits)
	if err != nil {
		return SeasonUpload{}, err
	}
	req, err := request.Bind[createSeasonUploadRequest](c)
	if err != nil {
		return SeasonUpload{}, err
	}
	if len(req.paths) != len(uploads) {
		return SeasonUpload{}, request.InvalidParam("文件路径与文件的份数不一致")
	}
	for i := range uploads {
		uploads[i].name = req.paths[i]
	}
	files, err := readUploads(uploads, limits)
	if err != nil {
		return SeasonUpload{}, err
	}
	entries := make([]SeasonEntry, len(req.entries))
	for i, e := range req.entries {
		entries[i] = SeasonEntry{Label: e.label, EpisodeID: e.episodeID, Files: make([]UploadedFile, len(e.files))}
		for j, f := range e.files {
			entries[i].Files[j] = files[f]
		}
	}
	folder, _, _ := strings.Cut(req.paths[0], "/") // 分组时已确认路径都以同一个文件夹名开头
	return SeasonUpload{SeasonID: req.SeasonID, Folder: folder, Entries: entries}, nil
}

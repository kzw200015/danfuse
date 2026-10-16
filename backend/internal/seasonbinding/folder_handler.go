package seasonbinding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// pickedFolderForm 文件夹的预览里选的文件夹：文件夹名与各条目的名称和文件哈希，代替文件本身。
// 两项同时给出或同时省略（编辑时不换文件夹）。
type pickedFolderForm struct {
	Folder  *string     `json:"folder"`
	Entries []entryForm `json:"entries"`

	picked *PickedFolder // parse 的结果，省略时为 nil
}

type entryForm struct {
	Label   string   `json:"label"`   // 条目名称，不带文件夹名
	SHA256s []string `json:"sha256s"` // 各份文件内容的 SHA-256，十六进制
}

// parse 校验并解析选的文件夹：required 时（新建）必须给出；文件夹名为空、没有条目时 400"请选择弹幕文件夹"，
// 条目名称为空或重复、条目没有文件、哈希不合法时 400。
func (f *pickedFolderForm) parse(required bool) error {
	if f.Folder == nil && f.Entries == nil {
		if required {
			return request.InvalidParam("请选择弹幕文件夹")
		}
		return nil
	}
	if f.Folder == nil || f.Entries == nil {
		return request.InvalidParam("文件夹名和条目要同时给出")
	}
	folder := strings.TrimSpace(*f.Folder)
	if folder == "" || len(f.Entries) == 0 {
		return request.InvalidParam("请选择弹幕文件夹")
	}
	p := &PickedFolder{Folder: folder, Entries: make([]EntryHashes, len(f.Entries))}
	labels := make(map[string]bool, len(f.Entries))
	for i, e := range f.Entries {
		switch {
		case e.Label == "":
			return request.InvalidParam("条目名称不能为空")
		case labels[e.Label]:
			return request.InvalidParam("条目名称重复：" + e.Label)
		case len(e.SHA256s) == 0:
			return request.InvalidParam(fmt.Sprintf("条目「%s」没有文件", e.Label))
		}
		labels[e.Label] = true
		p.Entries[i] = EntryHashes{Label: e.Label, SHA256s: make([][sha256.Size]byte, len(e.SHA256s))}
		for j, s := range e.SHA256s {
			sum, err := hex.DecodeString(s)
			if err != nil || len(sum) != sha256.Size {
				return request.InvalidParam("文件哈希不合法：" + s)
			}
			p.Entries[i].SHA256s[j] = [sha256.Size]byte(sum)
		}
	}
	f.picked = p
	return nil
}

type previewNewFolderRequest struct {
	SeasonID int64 `param:"id"`
	pickedFolderForm
	ruleForm
}

func (r *previewNewFolderRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	if err := r.pickedFolderForm.parse(true); err != nil {
		return err
	}
	return r.ruleForm.parse()
}

// PreviewNewFolder POST /api/seasons/:id/season-bindings/folder/preview {folder, entries:[{label, sha256s}], episodePatterns, mappingFrom, mappingTo}
// 预览新建文件夹的季绑定：不上传文件，返回只有一组的预览。不保存任何东西。
func (h *Handler) PreviewNewFolder(c *echo.Context) error {
	req, err := request.Bind[previewNewFolderRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewNewFolder(c.Request().Context(), req.SeasonID, *req.picked, FolderForm{Mapping: req.mapping, Rule: req.rule})
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type previewFolderEditRequest struct {
	ID int64 `param:"id"`
	pickedFolderForm
	ruleForm
}

func (r *previewFolderEditRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	if err := r.pickedFolderForm.parse(false); err != nil {
		return err
	}
	return r.ruleForm.parse()
}

// PreviewFolderEdit POST /api/season-bindings/:id/folder/preview {folder?, entries?, episodePatterns, mappingFrom, mappingTo}
// 预览保存文件夹的季绑定：不换文件夹时省略 folder 和 entries，按保存的条目算；current 为 true，removed 是保存后删除的槽位。
func (h *Handler) PreviewFolderEdit(c *echo.Context) error {
	req, err := request.Bind[previewFolderEditRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewFolderEdit(c.Request().Context(), req.ID, req.picked, FolderForm{Mapping: req.mapping, Rule: req.rule})
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

// parseFolderForm 文件夹的 multipart 里的 form 字段：集号规则与集号对应的 JSON {episodePatterns, mappingFrom, mappingTo}。
func parseFolderForm(form string) (FolderForm, error) {
	var rules ruleForm
	if err := json.Unmarshal([]byte(form), &rules); err != nil {
		return FolderForm{}, request.InvalidParam("表单格式不对，请刷新页面后重试")
	}
	if err := rules.parse(); err != nil {
		return FolderForm{}, err
	}
	return FolderForm{Mapping: rules.mapping, Rule: rules.rule}, nil
}

// createFolderRequest multipart 里除文件以外的字段。
type createFolderRequest struct {
	SeasonID int64  `param:"id"`
	Form     string `form:"form"` // 见 parseFolderForm

	form FolderForm
}

func (r *createFolderRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var err error
	r.form, err = parseFolderForm(r.Form)
	return err
}

// CreateFolder POST /api/seasons/:id/season-bindings/folder（multipart：files 的文件名是以文件夹名开头的相对路径，form 见 parseFolderForm）
// 创建文件夹的季绑定，当场建出各集的绑定，返回 201 和详情。上限（config.DanmakuFile 里按季的那组）、文件夹的结构、
// 表单有一项不对就 400，有一份文件解析失败就 422，什么都不保存。
func (h *Handler) CreateFolder(c *echo.Context) error {
	files, err := request.ReadFiles(c, h.folderLimits())
	if err != nil {
		return err
	}
	req, err := request.Bind[createFolderRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.CreateFolder(c.Request().Context(), req.SeasonID, uploadedFiles(files), req.form)
	if err != nil {
		return err
	}
	return response.Created(c, detail)
}

// saveFolderRequest multipart 里除文件以外的字段。
type saveFolderRequest struct {
	ID   int64  `param:"id"`
	Form string `form:"form"` // 见 parseFolderForm

	form FolderForm
}

func (r *saveFolderRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	var err error
	r.form, err = parseFolderForm(r.Form)
	return err
}

// SaveFolder PUT /api/season-bindings/:id/folder（multipart：files 可以省略，form 见 parseFolderForm）
// 保存文件夹的季绑定的表单，当场完成，返回 200 和详情：不带文件时只改集号规则、集号对应，带文件时换文件夹。错误同 CreateFolder。
func (h *Handler) SaveFolder(c *echo.Context) error {
	files, err := request.ReadOptionalFiles(c, h.folderLimits())
	if err != nil {
		return err
	}
	req, err := request.Bind[saveFolderRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.SaveFolder(c.Request().Context(), req.ID, uploadedFiles(files), req.form)
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

// folderLimits 上传文件夹的上限：份数与合计大小用按季的那组，单份大小与单集上传的相同。
func (h *Handler) folderLimits() request.FileLimits {
	return request.FileLimits{MaxFiles: h.upload.SeasonMaxFiles, MaxFileMB: h.upload.MaxFileMB, MaxUploadMB: h.upload.SeasonMaxUploadMB}
}

// uploadedFiles 上传的文件，文件名保留相对路径。
func uploadedFiles(files []request.File) []binding.UploadedFile {
	out := make([]binding.UploadedFile, len(files))
	for i, f := range files {
		out[i] = binding.UploadedFile(f)
	}
	return out
}

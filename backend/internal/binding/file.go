package binding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/bindingdb"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

var errNotFileBinding = apierr.ErrBadRequest.WithMessage("这个绑定不是用弹幕文件建的")

// UploadedFile 上传的一份弹幕文件。
type UploadedFile struct {
	// Name 上传的文件名，认不出时的提示用它；按季上传时是在所选文件夹里的相对路径（以文件夹名开头、用 / 分隔），
	// 存下的文件名取它的 base name
	Name string
	Data []byte
}

// FilesAdded 追加文件的结果。
type FilesAdded struct {
	Binding View  `json:"binding"`
	Files   int   `json:"files"`   // 新加入的份数
	Skipped int   `json:"skipped"` // 绑定里已有、跳过的份数（按内容判断）
	Added   int64 `json:"added"`   // 新增的弹幕条数
}

// FileView 绑定里的一份弹幕文件，不含内容。
type FileView struct {
	Name       string    `json:"name"`
	Size       int32     `json:"size"` // 字节
	UploadedAt time.Time `json:"uploadedAt"`
}

// parsedFile 解析好的一份弹幕文件。
type parsedFile struct {
	UploadedFile
	sum     [sha256.Size]byte
	danmaku []danmaku.Danmaku
}

// parseFiles 解析上传的全部弹幕文件、算出内容的哈希，在写入事务之前做完。
// 有一份认不出就整次返回 422，提示里用文件的 Name 写明是哪一份。
func parseFiles(files []UploadedFile) ([]parsedFile, error) {
	parsed := make([]parsedFile, len(files))
	for i, f := range files {
		items, err := danmakufile.Parse(f.Name, f.Data)
		if err != nil {
			return nil, fileAPIError(err)
		}
		parsed[i] = parsedFile{UploadedFile: f, sum: sha256.Sum256(f.Data), danmaku: items}
	}
	return parsed, nil
}

// fileAPIError 把 *danmakufile.Error 转成 422，提示用它的 Message，底层原因只进日志；其他错误原样返回。
func fileAPIError(err error) error {
	if fileErr, ok := errors.AsType[*danmakufile.Error](err); ok {
		return apierr.ErrUnprocessable.WithMessage(fileErr.Message).Wrap(fileErr.Err)
	}
	return err
}

// CreateFromFiles 用一组弹幕文件给一集创建绑定：一次上传的几份文件合起来是一个弹幕源。
// 先解析（有一份认不出就不创建），再在一个事务里锁住这一集、建出绑定、存下原文件、写入弹幕。
// 标题取第一份文件的文件名（去掉扩展名）。同一集可以有多个用弹幕文件建的绑定，不查重。
func (s *Service) CreateFromFiles(ctx context.Context, episodeID int64, files []UploadedFile) (View, error) {
	parsed, err := parseFiles(files)
	if err != nil {
		return View{}, err
	}
	var (
		binding  bindingdb.Binding
		newFiles int
		added    int64
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockEpisode(ctx, q, episodeID, errEpisodeNotFound); err != nil {
			return err
		}
		id, err := q.InsertFileBinding(ctx, bindingdb.InsertFileBindingParams{
			EpisodeID: episodeID,
			Title:     danmakufile.Title(files[0].Name),
		})
		if err != nil {
			return fmt.Errorf("insert file binding of episode %d: %w", episodeID, err)
		}
		binding, newFiles, added, err = addFiles(ctx, q, id, parsed)
		return err
	})
	if err != nil {
		return View{}, err
	}
	s.logFiles(ctx, "danmaku files added", binding, newFiles, added)
	return s.view(binding)
}

// SeasonEntry 按季上传的一个条目：所选文件夹下的一个子目录，或顶层的一份文件。它在目标集上建成一个绑定。
type SeasonEntry struct {
	Label     string         // 条目名称，也是建出的绑定的标题
	EpisodeID int64          // 目标集，取自预览
	Files     []UploadedFile // 文件名是在所选文件夹里的相对路径
}

// SeasonFiles 按季上传解析好的条目，由 ParseSeasonFiles 在写入事务之前得到，交给 CreateSeasonFilesInTx 写入。
type SeasonFiles struct {
	entries []SeasonEntry
	parsed  [][]parsedFile // 与 entries 一一对应
}

// SeasonFilesSaved CreateSeasonFilesInTx 写入的结果。
type SeasonFilesSaved struct {
	Bindings int   // 建出的绑定数
	Files    int   // 存下的文件份数（同一个条目里内容相同的只存一份）
	Added    int64 // 新增的弹幕总条数
}

// ParseSeasonFiles 按季上传在写入事务之前的准备：两个条目对到同一集时为 400；
// 解析全部文件，有一份认不出就整次 422，提示带着它的相对路径。
func ParseSeasonFiles(entries []SeasonEntry) (SeasonFiles, error) {
	targets := make(map[int64]string, len(entries)) // 目标集 → 对到它的条目
	for _, e := range entries {
		if other, ok := targets[e.EpisodeID]; ok {
			return SeasonFiles{}, apierr.ErrBadRequest.WithMessage(fmt.Sprintf("「%s」和「%s」对到了同一集，请重新预览", other, e.Label))
		}
		targets[e.EpisodeID] = e.Label
	}
	files := SeasonFiles{entries: entries, parsed: make([][]parsedFile, len(entries))}
	for i, e := range entries {
		var err error
		if files.parsed[i], err = parseFiles(e.Files); err != nil {
			return SeasonFiles{}, err
		}
	}
	return files, nil
}

// CreateSeasonFilesInTx 在按季上传的写入事务里，每个条目在它的目标集上建一个带来源季绑定的文件绑定，标题为条目名称：
// 逐个锁住目标集（已被删除或不属于这一季时为 404，调用方回滚，什么都不保存），建出绑定、存下原文件、写入弹幕。
// 目标集上已有绑定（包括用弹幕文件建的）时照常再建一个。调用方开事务，先锁住季、插入季绑定。
func (s *Service) CreateSeasonFilesInTx(ctx context.Context, tx pgx.Tx, seasonID, seasonBindingID int64, files SeasonFiles) (SeasonFilesSaved, error) {
	q := s.q.WithTx(tx)
	var saved SeasonFilesSaved
	for i, e := range files.entries {
		if _, err := q.LockSeasonEpisode(ctx, bindingdb.LockSeasonEpisodeParams{ID: e.EpisodeID, SeasonID: seasonID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return SeasonFilesSaved{}, apierr.ErrNotFound.WithMessage(fmt.Sprintf("「%s」的目标集已被删除或不属于这一季，请重新预览", e.Label))
			}
			return SeasonFilesSaved{}, fmt.Errorf("lock episode %d of season %d: %w", e.EpisodeID, seasonID, err)
		}
		id, err := q.InsertFileBinding(ctx, bindingdb.InsertFileBindingParams{
			EpisodeID: e.EpisodeID, Title: e.Label, SeasonBindingID: &seasonBindingID,
		})
		if err != nil {
			return SeasonFilesSaved{}, fmt.Errorf("insert file binding of episode %d: %w", e.EpisodeID, err)
		}
		_, newFiles, added, err := addFiles(ctx, q, id, files.parsed[i])
		if err != nil {
			return SeasonFilesSaved{}, err
		}
		saved.Bindings++
		saved.Files += newFiles
		saved.Added += added
	}
	return saved, nil
}

// AppendFiles 追加文件：往用弹幕文件建的绑定里再加入弹幕文件，只增不删。
// 绑定里已有内容相同的文件时跳过它，全部跳过时什么都不改（Files 为 0）。弹幕按原始 ID 去重。
func (s *Service) AppendFiles(ctx context.Context, id int64, files []UploadedFile) (FilesAdded, error) {
	if err := s.checkFileBinding(ctx, id); err != nil {
		return FilesAdded{}, err
	}
	parsed, err := parseFiles(files)
	if err != nil {
		return FilesAdded{}, err
	}
	var (
		b        bindingdb.Binding
		newFiles int
		added    int64
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, id); err != nil {
			return err
		}
		var err error
		b, newFiles, added, err = addFiles(ctx, q, id, parsed)
		return err
	})
	if err != nil {
		return FilesAdded{}, err
	}
	s.logFiles(ctx, "danmaku files added", b, newFiles, added)
	view, err := s.view(b)
	if err != nil {
		return FilesAdded{}, err
	}
	return FilesAdded{Binding: view, Files: newFiles, Skipped: len(files) - newFiles, Added: added}, nil
}

// addFiles 在写入事务里把解析好的文件加入绑定：存下原文件，文件名取 base name（绑定里已有内容相同的跳过），
// 写入新加入的文件的弹幕，再更新绑定的文件份数与弹幕计数。调用方已在同一个事务里锁住或刚插入这个绑定。
func addFiles(ctx context.Context, q *bindingdb.Queries, bindingID int64, files []parsedFile) (bindingdb.Binding, int, int64, error) {
	var (
		items    []danmaku.Danmaku
		newFiles int
	)
	for _, f := range files {
		n, err := q.InsertBindingFile(ctx, bindingdb.InsertBindingFileParams{
			BindingID: bindingID,
			Name:      path.Base(f.Name),
			Sha256:    f.sum[:],
			Size:      int32(len(f.Data)),
			Content:   f.Data,
		})
		if err != nil {
			return bindingdb.Binding{}, 0, 0, fmt.Errorf("insert file %q of binding %d: %w", f.Name, bindingID, err)
		}
		if n > 0 {
			newFiles++
			items = append(items, f.danmaku...)
		}
	}
	if err := q.AddBindingFileCount(ctx, bindingdb.AddBindingFileCountParams{ID: bindingID, Files: int32(newFiles)}); err != nil {
		return bindingdb.Binding{}, 0, 0, fmt.Errorf("count files of binding %d: %w", bindingID, err)
	}
	b, added, err := writeDanmaku(ctx, q, bindingID, items, false)
	return b, newFiles, added, err
}

// Reparse 重新解析：按绑定保存的全部弹幕文件重新解析，在一个事务里替换现有弹幕。
// 文件逐份读出、解析、写入，不一次读进全部文件。有一份解析不了时返回 422，什么都不改。
func (s *Service) Reparse(ctx context.Context, id int64) (View, error) {
	if err := s.checkFileBinding(ctx, id); err != nil {
		return View{}, err
	}
	var (
		b      bindingdb.Binding
		added  int64
		files  int
		latest int32
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, id); err != nil {
			return err
		}
		if err := q.DeleteDanmaku(ctx, id); err != nil {
			return fmt.Errorf("delete danmaku of binding %d: %w", id, err)
		}
		list, err := q.ListBindingFiles(ctx, id)
		if err != nil {
			return fmt.Errorf("list files of binding %d: %w", id, err)
		}
		files = len(list)
		for _, f := range list {
			content, err := q.GetBindingFileContent(ctx, f.ID)
			if err != nil {
				return fmt.Errorf("get file %d of binding %d: %w", f.ID, id, err)
			}
			items, err := danmakufile.Parse(f.Name, content)
			if err != nil {
				return fileAPIError(err)
			}
			n, err := insertDanmaku(ctx, q, id, items)
			if err != nil {
				return err
			}
			added += n
			latest = max(latest, latestTime(items))
		}
		b, err = recordDanmaku(ctx, q, id, true, added, latest)
		return err
	})
	if err != nil {
		return View{}, err
	}
	s.logFiles(ctx, "danmaku files reparsed", b, files, added)
	return s.view(b)
}

// ListFiles 绑定里的弹幕文件，按加入的顺序排列。
func (s *Service) ListFiles(ctx context.Context, id int64) ([]FileView, error) {
	if err := s.checkFileBinding(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.q.ListBindingFiles(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list files of binding %d: %w", id, err)
	}
	views := make([]FileView, len(rows))
	for i, r := range rows {
		views[i] = FileView{Name: r.Name, Size: r.Size, UploadedAt: r.UploadedAt}
	}
	return views, nil
}

// checkFileBinding 确认绑定是用弹幕文件建的：不存在时 404，是贴链接建的时 400。kind 建出后不变，在事务之外确认即可。
func (s *Service) checkFileBinding(ctx context.Context, id int64) error {
	b, err := s.getBinding(ctx, id)
	if err != nil {
		return err
	}
	if b.Kind != kindFile {
		return errNotFileBinding
	}
	return nil
}

// logFiles 加入、重新解析弹幕文件之后记一条 info 日志。
func (s *Service) logFiles(ctx context.Context, msg string, b bindingdb.Binding, files int, added int64) {
	s.logger.LogAttrs(ctx, slog.LevelInfo, msg,
		slog.Int64("binding_id", b.ID), slog.Int("files", files), slog.Int64("added", added))
}

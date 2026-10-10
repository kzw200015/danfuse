package seasonbinding

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// SeasonUploadPreview 按季上传的预览：各个条目按集号规则认出的序号，顺序与请求里的条目名称相同。
// 条目的字段与季绑定预览的条目相同。
type SeasonUploadPreview struct {
	Items []PreviewItem `json:"items"`
}

// PreviewSeasonUpload 按季上传的预览：把各个条目当作按规则编号的合集里的条目（名称就是标签），
// 用集号规则认出序号，与季绑定预览同一套匹配、NFKC 回退和"集号重复"的标注。名称相同的条目各自保留。
// 集号对应、对到哪一集由调用方按序号现算。不写库；季不存在为 404。
func (s *Service) PreviewSeasonUpload(ctx context.Context, seasonID int64, labels []string, rule source.EpisodeRule) (SeasonUploadPreview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return SeasonUploadPreview{}, err
	}
	col := source.Collection{NumberedByRule: true, Items: make([]source.CollectionItem, len(labels))}
	for i, label := range labels {
		// 条目没有弹幕源，ref 只用来让每个条目各不相同（NumberItems 会去掉 ref 重复的条目）
		col.Items[i] = source.CollectionItem{Ref: source.Ref(strconv.Itoa(i)), Label: label}
	}
	items := source.NumberItems(col, rule)
	preview := SeasonUploadPreview{Items: make([]PreviewItem, len(items))}
	for i, it := range items {
		item := PreviewItem{Label: it.Label, Reason: nullIfEmpty(it.Unmatched)}
		if it.Unmatched == "" {
			item.Number = new(it.Number)
		}
		preview.Items[i] = item
	}
	return preview, nil
}

// SeasonFilesCreated 按季上传的结果。
type SeasonFilesCreated struct {
	Bindings int   `json:"bindings"` // 建出的绑定数
	Added    int64 `json:"added"`    // 新增的弹幕总条数
}

// CreateFromSeasonFiles 按季上传：留下一个名为 folder（所选的文件夹名）的文件夹的季绑定，
// 每个条目在它的目标集上建一个指向它的文件绑定，标题为条目名称。
// 先在事务之外由 binding.Service.ParseSeasonFiles 解析全部文件（两个条目对到同一集为 400，有一份认不出就整次 422，提示带着它的相对路径），
// 再在一个事务里锁住季、插入季绑定，把事务交给 binding.Service.CreateSeasonFilesInTx 逐个锁住目标集、建出绑定、
// 存下原文件、写入弹幕（与补建一样先锁季）。季不存在为 404，保存前被删除为 404"这一季已被删除"；
// 目标集已被删除或不属于这一季时整次 404。失败时季绑定和绑定都不保存。
func (s *Service) CreateFromSeasonFiles(ctx context.Context, seasonID int64, folder string, entries []binding.SeasonEntry) (SeasonFilesCreated, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return SeasonFilesCreated{}, err
	}
	files, err := s.bindings.ParseSeasonFiles(entries)
	if err != nil {
		return SeasonFilesCreated{}, err
	}
	var (
		id    int64
		saved binding.SeasonFilesSaved
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 先锁季，再插入季绑定、锁集：顺序与删季的级联（季 → 集 → 季绑定）不同，
		// 但和补建一样第一句锁季，删季要等这个事务提交，不会死锁
		if err := lockSeason(ctx, q, seasonID, errSeasonDeleted); err != nil {
			return err
		}
		var err error
		id, err = q.InsertFolderSeasonBinding(ctx, seasonbindingdb.InsertFolderSeasonBindingParams{SeasonID: seasonID, Title: folder})
		if err != nil {
			return fmt.Errorf("insert folder season binding of season %d: %w", seasonID, err)
		}
		saved, err = s.bindings.CreateSeasonFilesInTx(ctx, tx, seasonID, id, files)
		return err
	})
	if err != nil {
		return SeasonFilesCreated{}, err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "season danmaku files added",
		slog.Int64("season_id", seasonID), slog.Int64("season_binding_id", id), slog.Int("bindings", saved.Bindings),
		slog.Int("files", saved.Files), slog.Int64("added", saved.Added))
	return SeasonFilesCreated{Bindings: saved.Bindings, Added: saved.Added}, nil
}

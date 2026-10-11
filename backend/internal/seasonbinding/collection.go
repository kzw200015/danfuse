package seasonbinding

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errSeasonBindingExists = apierr.ErrConflict.WithMessage("这一季已经绑定过这个合集")
	errKindRequired        = apierr.ErrBadRequest.WithMessage("链接对应多个合集，请选择一个")
	errKindNotFound        = apierr.ErrBadRequest.WithMessage("链接里没有这种合集，请重新预览")
)

// CollectionForm 合集的表单：贴的链接、选的候选、集号对应、集号规则。
type CollectionForm struct {
	Link           string // 原样保存，编辑时预填
	CollectionKind string // 选哪个候选；链接只有一个候选时可以为空
	Mapping        source.Mapping
	Rule           source.EpisodeRule // 集号规则；合集不按规则编号时也照样保存，不起作用
}

// CreateCollection 创建合集的季绑定：在事务之外识别链接、按 collectionKind 选候选、查重（409）、列出合集，
// 事务里写季绑定和这次列出的结果，随即在后台补建，返回详情。季不存在为 404，保存前被删除为 404"这一季已被删除"；
// 同时两次绑定同一个合集时，后提交的撞上唯一约束返回 409。其余错误见 resolve。新建的季绑定开着追更。
func (s *Service) CreateCollection(ctx context.Context, seasonID int64, f CollectionForm) (Detail, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Detail{}, err
	}
	r, err := s.resolve(ctx, seasonID, nil, f.Link, f.CollectionKind)
	if err != nil {
		return Detail{}, err
	}

	var id int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 锁住这一季到提交：之后的删除要等这个事务提交，再连同季绑定一起删掉
		if err := lockSeason(ctx, q, seasonID, errSeasonDeleted); err != nil {
			return err
		}
		var err error
		id, err = q.InsertSeasonBinding(ctx, seasonbindingdb.InsertSeasonBindingParams{
			SeasonID:        seasonID,
			Adapter:         r.adapter.ID(),
			Ref:             r.ref,
			Link:            f.Link,
			Title:           r.col.Title,
			MappingFrom:     int32(f.Mapping.From),
			MappingTo:       int32(f.Mapping.To),
			EpisodePatterns: f.Rule.Patterns(),
			NumberedByRule:  r.col.NumberedByRule,
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errSeasonBindingExists
			}
			return fmt.Errorf("insert season binding of season %d: %w", seasonID, err)
		}
		return saveItems(ctx, q, id, r.col)
	})
	if err != nil {
		return Detail{}, err
	}
	s.startBackfill(ctx, id)
	return s.Get(ctx, id)
}

// resolved 按链接和种类选定、列出的合集。
type resolved struct {
	adapter source.Adapter
	ref     source.CollectionRef
	col     source.Collection
}

// resolve 创建、保存时重新识别链接、按 kind 选候选、查重（这一季已经绑定过这个合集时 409，保存时不算 self 自己），
// 再列出合集；识别与列出共用 source.FetchTimeout。链接无法识别、不能作为合集绑定、选不出候选为 400；
// 合集不存在为 422；上游故障、限流、超时为 502。
func (s *Service) resolve(ctx context.Context, seasonID int64, self *int64, link, kind string) (resolved, error) {
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, link)
	if err != nil {
		return resolved{}, binding.SourceAPIError(err)
	}
	chosen, err := chooseCandidate(candidates, kind)
	if err != nil {
		return resolved{}, err
	}
	// 只是省掉一次注定 409 的列出；并发时以写入事务里的唯一约束为准
	key := seasonbindingdb.SeasonBindingExistsParams{SeasonID: seasonID, Adapter: adapter.ID(), Ref: chosen.Ref, ExcludeID: self}
	switch exists, err := s.q.SeasonBindingExists(ctx, key); {
	case err != nil:
		return resolved{}, fmt.Errorf("check season binding of season %d: %w", seasonID, err)
	case exists:
		return resolved{}, errSeasonBindingExists
	}
	col, err := adapter.ListCollection(netCtx, chosen.Ref)
	if err != nil {
		return resolved{}, binding.SourceAPIError(err)
	}
	return resolved{adapter: adapter, ref: chosen.Ref, col: col}, nil
}

// chooseCandidate 按 kind 选候选：只有一个候选时 kind 可以为空；有多个候选而没给 kind、或 kind 不在候选里时为 400。
func chooseCandidate(candidates []source.CollectionCandidate, kind string) (source.CollectionCandidate, error) {
	if kind == "" {
		if len(candidates) != 1 {
			return source.CollectionCandidate{}, errKindRequired
		}
		return candidates[0], nil
	}
	i := slices.IndexFunc(candidates, func(c source.CollectionCandidate) bool { return c.Kind == kind })
	if i < 0 {
		return source.CollectionCandidate{}, errKindNotFound
	}
	return candidates[i], nil
}

// saveItems 在写入事务里保存一次列出的结果（season_binding_items）：按规范化的 ref 去重（保留第一个），
// 按弹幕源 upsert（位置从 1 开始），删掉合集里已经没有的。number 只存平台给的集号：按规则编号的合集、
// 对不上的条目存为 null，读取时再按季绑定的集号规则编号（见 storedCollection）。
func saveItems(ctx context.Context, q *seasonbindingdb.Queries, id int64, col source.Collection) error {
	p := seasonbindingdb.UpsertSeasonBindingItemsParams{
		SeasonBindingID: id, Refs: []string{}, Positions: []int32{}, Numbers: []int32{}, Labels: []string{},
	}
	seen := make(map[string]bool, len(col.Items))
	for _, it := range col.Items {
		ref := source.CanonicalRef(it.Ref)
		if seen[ref] {
			continue
		}
		seen[ref] = true
		number := int32(-1) // 存为 null
		if !col.NumberedByRule && it.Unmatched == "" {
			number = int32(it.Number)
		}
		p.Refs = append(p.Refs, ref)
		p.Positions = append(p.Positions, int32(len(p.Refs)))
		p.Numbers = append(p.Numbers, number)
		p.Labels = append(p.Labels, it.Label)
	}
	if err := q.DeleteStaleSeasonBindingItems(ctx, seasonbindingdb.DeleteStaleSeasonBindingItemsParams{SeasonBindingID: id, Refs: p.Refs}); err != nil {
		return fmt.Errorf("delete stale items of season binding %d: %w", id, err)
	}
	if err := q.UpsertSeasonBindingItems(ctx, p); err != nil {
		return fmt.Errorf("upsert items of season binding %d: %w", id, err)
	}
	return nil
}

// saveListed 在写入事务里写入一次列出的结果：季绑定恢复为正常、更新合集的标题与是否按规则编号，保存条目，
// 返回更新后的季绑定（同一个事务里随后派生用它的表单）。季绑定已被删除时返回 gone。
func saveListed(ctx context.Context, q *seasonbindingdb.Queries, id int64, col source.Collection, gone error) (seasonbindingdb.SeasonBinding, error) {
	sb, err := q.RecordSeasonBindingListed(ctx, seasonbindingdb.RecordSeasonBindingListedParams{
		ID: id, Title: col.Title, NumberedByRule: col.NumberedByRule,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return seasonbindingdb.SeasonBinding{}, gone
		}
		return seasonbindingdb.SeasonBinding{}, fmt.Errorf("record season binding %d listed: %w", id, err)
	}
	return sb, saveItems(ctx, q, id, col)
}

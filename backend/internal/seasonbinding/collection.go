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
	errSaveWhileBackfill   = apierr.ErrConflict.WithMessage("正在补建或保存，稍后再试")
)

// CollectionForm 合集的表单：贴的链接、选的候选、集号对应、集号规则。
type CollectionForm struct {
	Link           string // 原样保存，编辑时预填
	CollectionKind string // 选哪个候选；链接只有一个候选时可以为空；预览列出全部候选，不看它
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

// SaveCollection 保存合集的季绑定的表单，每次都走一遍，不比较表单改没改（没改也能保存，用来清掉补建留下的空槽位）：
//  1. 拿这个季绑定的补建租约，拿不到（正在补建或保存）为 409，持有到写入提交；
//  2. 重新识别链接、按 collectionKind 选候选、查重、列出合集（见 resolve）。删除以这次列出的结果为准，不和预览核对；
//  3. 一个事务里锁季、FOR UPDATE 锁季绑定，写表单（换成另一个合集时清掉上次检查时间），保存这次列出的结果，
//     派生，删掉没有条目的槽位（DeleteSlotsInTx）。替换和新建不在这里做；
//  4. 释放租约，随即在后台补建，返回详情。
//
// 不存在为 404（保存前季或季绑定被删除也是），文件夹的季绑定为 400；同一季已经绑定过这个合集为 409。
func (s *Service) SaveCollection(ctx context.Context, id int64, f CollectionForm) (Detail, error) {
	sb, err := s.getCollection(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	lease, err := s.tryBackfillLease(ctx, id)
	if err != nil {
		if errors.Is(err, errBackfillRunning) {
			return Detail{}, errSaveWhileBackfill
		}
		return Detail{}, err
	}
	defer lease.Release()       // 出错返回时；写完之后在补建之前就释放
	leaseCtx := lease.Context() // 租约丢失时随即停下

	r, err := s.resolve(leaseCtx, sb.SeasonID, &id, f.Link, f.CollectionKind)
	if err != nil {
		return Detail{}, err
	}
	var removed int
	err = pgx.BeginFunc(leaseCtx, s.pool, func(tx pgx.Tx) error {
		var err error
		removed, err = s.saveCollectionTx(leaseCtx, tx, sb.SeasonID, id, f, r)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	lease.Release() // 随后的补建才拿得到租约
	s.logger.InfoContext(ctx, "season binding saved",
		"season_binding_id", id, "adapter", r.adapter.ID(), "ref", string(r.ref), "removed_bindings", removed)
	s.startBackfill(ctx, id)
	return s.Get(ctx, id)
}

// saveCollectionTx SaveCollection 的写入事务，返回删掉的槽位数。
func (s *Service) saveCollectionTx(ctx context.Context, tx pgx.Tx, seasonID, id int64, f CollectionForm, r resolved) (int, error) {
	q := s.q.WithTx(tx)
	if _, err := lockSeasonBinding(ctx, q, seasonID, id); err != nil {
		return 0, err
	}
	err := q.SaveCollectionForm(ctx, seasonbindingdb.SaveCollectionFormParams{
		ID:              id,
		Adapter:         r.adapter.ID(),
		Ref:             r.ref,
		Link:            f.Link,
		MappingFrom:     int32(f.Mapping.From),
		MappingTo:       int32(f.Mapping.To),
		EpisodePatterns: f.Rule.Patterns(),
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			return 0, errSeasonBindingExists
		}
		return 0, fmt.Errorf("save form of season binding %d: %w", id, err)
	}
	if _, err := saveListed(ctx, q, id, r.col, errSeasonBindingNotFound); err != nil {
		return 0, err
	}
	episodes, err := episodeSlots(ctx, q, seasonID, &id, r.adapter.ID())
	if err != nil {
		return 0, err
	}
	episodeIDs := Derive(collectionEntries(source.NumberItems(r.col, f.Rule)), f.Mapping, episodes).emptyEpisodeIDs()
	if len(episodeIDs) == 0 {
		return 0, nil
	}
	return len(episodeIDs), s.bindings.DeleteSlotsInTx(ctx, tx, id, episodeIDs)
}

// PreviewNewCollection 新建的预览：识别链接、列出每个候选合集，按 f 里的集号规则和集号对应派生（这一季还没有这个季绑定的槽位），
// 每个候选一组。不看 f.CollectionKind，不写库，不缓存。季不存在为 404，其余错误见 listCandidates。
func (s *Service) PreviewNewCollection(ctx context.Context, seasonID int64, f CollectionForm) (Preview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Preview{}, err
	}
	adapter, listed, err := s.listCandidates(ctx, f.Link)
	if err != nil {
		return Preview{}, err
	}
	episodes, err := episodeSlots(ctx, s.q, seasonID, nil, adapter.ID())
	if err != nil {
		return Preview{}, err
	}
	return collectionPreview(adapter, listed, f, episodes, nil)
}

// PreviewCollectionEdit 编辑的预览：同新建的预览，另外按这个季绑定现在的槽位派生（保存后要替换、删除哪些），
// 标出 ref 与保存的合集相同的候选。不写库。季绑定不存在为 404，文件夹的季绑定为 400，其余错误见 listCandidates。
func (s *Service) PreviewCollectionEdit(ctx context.Context, id int64, f CollectionForm) (Preview, error) {
	sb, err := s.getCollection(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	adapter, listed, err := s.listCandidates(ctx, f.Link)
	if err != nil {
		return Preview{}, err
	}
	episodes, err := episodeSlots(ctx, s.q, sb.SeasonID, &sb.ID, adapter.ID())
	if err != nil {
		return Preview{}, err
	}
	var current source.CollectionRef
	if *sb.Adapter == adapter.ID() {
		current = sb.Ref
	}
	return collectionPreview(adapter, listed, f, episodes, current)
}

// listedCandidate 链接识别出的一个候选合集，连同这次列出的内容。
type listedCandidate struct {
	source.CollectionCandidate
	col source.Collection
}

// listCandidates 识别链接、列出每个候选合集，识别与列出共用 source.FetchTimeout。
// 链接无法识别、不能作为合集绑定为 400；合集不存在为 422；上游故障、限流、超时为 502。
func (s *Service) listCandidates(ctx context.Context, link string) (source.Adapter, []listedCandidate, error) {
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, link)
	if err != nil {
		return nil, nil, binding.SourceAPIError(err)
	}
	listed := make([]listedCandidate, len(candidates))
	for i, c := range candidates {
		col, err := adapter.ListCollection(netCtx, c.Ref)
		if err != nil {
			return nil, nil, binding.SourceAPIError(err)
		}
		listed[i] = listedCandidate{CollectionCandidate: c, col: col}
	}
	return adapter, listed, nil
}

// collectionPreview 每个候选按 f 的集号规则编号、按 f 的集号对应和 episodes 派生，填上候选的信息；
// current 不为 nil 时标出 ref 与它相同的候选（编辑时保存的合集）。
func collectionPreview(adapter source.Adapter, listed []listedCandidate, f CollectionForm, episodes map[int]Episode, current source.CollectionRef) (Preview, error) {
	p := Preview{Candidates: make([]Candidate, len(listed))}
	for i, l := range listed {
		d, err := adapter.DescribeCollection(l.Ref)
		if err != nil {
			return Preview{}, fmt.Errorf("describe collection %s: %w", l.Ref, err)
		}
		c := newCandidate(Derive(collectionEntries(source.NumberItems(l.col, f.Rule)), f.Mapping, episodes))
		c.CollectionKind = new(l.Kind)
		c.Title = l.col.Title
		c.SourceURL, c.SourceLabel = new(d.URL), new(d.Label)
		c.Finished = l.col.Finished
		c.NumberedByRule = l.col.NumberedByRule
		c.Current = current != nil && source.CanonicalRef(source.Ref(l.Ref)) == source.CanonicalRef(source.Ref(current))
		p.Candidates[i] = c
	}
	return p, nil
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

// saveItems 在写入事务里保存一次列出的结果（season_binding_items）：按规范化的 ref 去重（source.UniqueRefs），
// 按弹幕源 upsert（位置从 1 开始），删掉合集里已经没有的。number 只存平台给的集号：按规则编号的合集、
// 对不上的条目存为 null，读取时再按季绑定的集号规则编号（见 storedCollection）。
func saveItems(ctx context.Context, q *seasonbindingdb.Queries, id int64, col source.Collection) error {
	p := seasonbindingdb.UpsertSeasonBindingItemsParams{
		SeasonBindingID: id, Refs: []string{}, Positions: []int32{}, Numbers: []int32{}, Labels: []string{},
	}
	for _, it := range source.UniqueRefs(col.Items) {
		number := int32(-1) // 存为 null
		if !col.NumberedByRule && it.Unmatched == "" {
			number = int32(it.Number)
		}
		p.Refs = append(p.Refs, source.CanonicalRef(it.Ref))
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

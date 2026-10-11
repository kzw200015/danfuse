package seasonbinding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/background"
	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errSeasonNotFound        = apierr.ErrNotFound.WithMessage("季不存在")
	errSeasonDeleted         = apierr.ErrNotFound.WithMessage("这一季已被删除")
	errSeasonBindingExists   = apierr.ErrConflict.WithMessage("这一季已经绑定过这个合集")
	errSeasonBindingNotFound = apierr.ErrNotFound.WithMessage("季绑定不存在")
	errBackfillRunning       = apierr.ErrConflict.WithMessage("正在补建")
	errChangeWhileBackfill   = apierr.ErrConflict.WithMessage("正在补建，补建结束后再换合集")
	errKindRequired          = apierr.ErrBadRequest.WithMessage("链接对应多个合集，请选择一个")
	errKindNotFound          = apierr.ErrBadRequest.WithMessage("链接里没有这种合集，请重新预览")
	errFolderDeleteOnly      = apierr.ErrBadRequest.WithMessage("文件夹的季绑定只能删除")
)

// 季绑定的种类（season_bindings.kind，见 docs/adr/0008）。
const (
	kindCollection = "collection" // 合集的季绑定：按集号对应补建出链接绑定，可以追更
	kindFolder     = "folder"     // 文件夹的季绑定：按季上传留下，记着这次上传建出的文件绑定，只能删除
)

// Service 季绑定：在季上绑定一个合集，按集号对应为各集补建出普通的绑定；追更时定时补建。
// 按季上传（season_upload.go）也归它：留下一个文件夹的季绑定，建出的文件绑定由 binding 在同一个事务里写入；
// 文件夹的季绑定只能查看和删除，补建、追更、改集号对应和集号规则都只适用于合集的季绑定。
// 建出的绑定之后的重新拉取与季绑定无关，见定时拉取（binding.ScheduledFetchService）。
//
// 生命周期同同步（catalog.SyncService）：App.Run 运行 Run(ctx)（循环是 background.Loop），补建都用 Run 的 ctx（应用级），不用 HTTP 请求的 ctx；
// 手动触发（创建、立即补建、改集号对应、打开追更）经 Run 的循环立即在后台开始，不同季绑定的补建互不等待；
// 追更的扫描由 Run 的循环每隔 follow.scan_interval 在后台开始一次、一次补建一个，不等上一次扫描做完；时间规则见 config.Follow。
// ctx 取消时进行中的扫描和补建停下，Run 等它们返回，之后 main 才关闭连接池。
//
// "补建中"以按季绑定的租约（database.LeaseSeasonBackfill）为准，多实例同样成立，季绑定上不存运行状态。
// 网络请求都在事务之外；写入事务先锁季、再锁季绑定，删除季绑定时排队，之后补建再也锁不到它，随即结束。
type Service struct {
	pool     *pgxpool.Pool // 开事务，拿按季绑定的租约
	q        *seasonbindingdb.Queries
	sources  *source.Registry
	bindings *binding.Service // 补建出的绑定由它写入
	follow   config.Follow    // 追更的时间规则
	logger   *slog.Logger
	loop     *background.Loop // Run 的循环：追更的扫描与手动触发，进行中的扫描和补建
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, bindings *binding.Service, follow config.Follow, logger *slog.Logger) *Service {
	return &Service{
		pool:     pool,
		q:        seasonbindingdb.New(pool),
		sources:  sources,
		bindings: bindings,
		follow:   follow,
		logger:   logger,
		loop:     background.NewLoop(),
	}
}

// View 季绑定的 JSON。不对外暴露原始的合集 ref，sourceUrl / sourceLabel 由适配器的 DescribeCollection 生成。
// 只属于合集的季绑定的字段（adapter 到 lastCheckedAt）对文件夹的季绑定为 null。
type View struct {
	ID       int64  `json:"id"`
	SeasonID int64  `json:"seasonId"`
	Kind     string `json:"kind"`  // collection | folder
	Title    string `json:"title"` // 合集的季绑定为合集标题，每次检查更新；文件夹的季绑定为所选的文件夹名

	Adapter *string `json:"adapter"`
	// Link 创建或换合集时贴的链接，编辑时预填；记下链接之前建的季绑定为 null
	Link        *string `json:"link"`
	SourceURL   *string `json:"sourceUrl"`
	SourceLabel *string `json:"sourceLabel"` // 例如"B 站番剧 ss41410"，含合集的种类
	Finished    *bool   `json:"finished"`    // 平台上已完结
	MappingFrom *int32  `json:"mappingFrom"` // 集号对应：合集第 mappingFrom 集为本地第 mappingTo 集
	MappingTo   *int32  `json:"mappingTo"`
	// NumberedByRule 合集的序号由集号规则从条目的标签认出（投稿合集、多 P 投稿），上次列出时由适配器给出
	NumberedByRule  *bool      `json:"numberedByRule"`
	EpisodePatterns []string   `json:"episodePatterns"` // 集号规则：一组正则，取法见 source.EpisodeRule
	LastError       *string    `json:"lastError"`       // 上次检查结束时的错误
	LastCheckedAt   *time.Time `json:"lastCheckedAt"`   // 上次检查的开始时间

	Follow       bool   `json:"follow"`       // 文件夹的季绑定恒为 false
	Status       string `json:"status"`       // active | dead；文件夹的季绑定恒为 active
	Running      bool   `json:"running"`      // 正在补建；文件夹的季绑定恒为 false
	BindingCount int32  `json:"bindingCount"` // 它建出的、现存的绑定数
	// CreatedAt 创建时间：合集的季绑定为创建的时间，文件夹的季绑定为上传的时间
	CreatedAt time.Time `json:"createdAt"`
}

// Detail 季绑定的详情：另有条目表，显示的是上次检查时的合集内容，不实时请求平台。
type Detail struct {
	View
	Items []ItemView `json:"items"` // 按在合集里的顺序；文件夹的季绑定为空数组
}

// 条目的状态，读取时算出，不存储。
const (
	itemBound          = "bound"          // 已建绑定：它建出的绑定还在
	itemAlreadyBound   = "alreadyBound"   // 集上已有这个弹幕源的绑定，不是它建出的（手动绑的、别的季绑定建的），补建跳过
	itemBindingDeleted = "bindingDeleted" // 处理过，但那一集上已经没有这个弹幕源的绑定（用户删掉了）
	itemUnmatched      = "unmatched"      // 对不上，reason 为原因
	itemBeforeStart    = "beforeStart"    // 序号在集号对应的起点之前
	itemWaitingEpisode = "waitingEpisode" // 目录里还没有对应的集
	itemPending        = "pending"        // 待补建
	itemFailed         = "failed"         // 最近一次补建失败，reason 为原因，下次补建再试
)

// ItemView 条目表的一行。
type ItemView struct {
	Label  string  `json:"label"`
	Number *int32  `json:"number"` // 合集序号；对不上时为 null
	State  string  `json:"state"`
	Reason *string `json:"reason"` // 对不上、失败的原因
	// EpisodeNumber 处理过的条目为绑定建在的那一集；其余能算出对应的集时为对应的集号
	EpisodeNumber *int32     `json:"episodeNumber"`
	LastErrorAt   *time.Time `json:"lastErrorAt"` // 失败时为失败的时间
}

// CollectionPreview 预览：链接识别出的各个候选合集，不保存任何东西。
type CollectionPreview struct {
	Candidates []PreviewCandidate `json:"candidates"`
}

type PreviewCandidate struct {
	CandidateInfo
	Items []PreviewItem `json:"items"`
}

// CandidateInfo 预览里一个候选合集的信息，创建与编辑的预览共用。
type CandidateInfo struct {
	Kind        string `json:"kind"` // 创建、换合集时传回，用来选候选
	Title       string `json:"title"`
	SourceURL   string `json:"sourceUrl"`
	SourceLabel string `json:"sourceLabel"`
	Finished    bool   `json:"finished"`
	// NumberedByRule 序号由集号规则从条目的标签认出：预览按请求里的集号规则认，创建、保存时传同一个规则
	NumberedByRule bool `json:"numberedByRule"`
}

// PreviewItem 预览的条目，字段与季绑定的条目相同；重复的序号已标为对不上（number 为 null、reason 为"集号重复"）。
type PreviewItem struct {
	Label  string  `json:"label"`
	Number *int    `json:"number"`
	Reason *string `json:"reason"` // 对不上的原因
}

// ChangePreview 编辑合集的季绑定时的预览：链接识别出的各个候选合集，不保存任何东西。
type ChangePreview struct {
	Candidates []ChangeCandidate `json:"candidates"`
}

type ChangeCandidate struct {
	CandidateInfo
	Current bool `json:"current"` // 季绑定现在就绑着这个合集
	// RemovedBindings 换成这个合集时会删掉的绑定数，即季绑定建出的、弹幕源不在这个合集里的绑定；
	// 就是现在的合集时为 0（不换合集不删绑定）
	RemovedBindings int32        `json:"removedBindings"`
	Items           []ChangeItem `json:"items"`
}

// ChangeItem 编辑时预览的条目。
type ChangeItem struct {
	PreviewItem
	// HandledEpisode 季绑定处理过这个条目时为绑定建在的那一集：改集号对应、集号规则、换合集都不动它；其余为 null
	HandledEpisode *int32 `json:"handledEpisode"`
}

// Preview 识别链接、列出每个候选合集，按集号规则认出按规则编号的合集的序号。不写库。
// 季不存在为 404；其余错误见 listCandidates。
func (s *Service) Preview(ctx context.Context, seasonID int64, link string, rule source.EpisodeRule) (CollectionPreview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return CollectionPreview{}, err
	}
	adapter, listed, err := s.listCandidates(ctx, link)
	if err != nil {
		return CollectionPreview{}, err
	}
	preview := CollectionPreview{Candidates: make([]PreviewCandidate, len(listed))}
	for i, l := range listed {
		info, err := candidateInfo(adapter, l)
		if err != nil {
			return CollectionPreview{}, err
		}
		preview.Candidates[i] = PreviewCandidate{CandidateInfo: info, Items: previewItems(source.NumberItems(l.col, rule))}
	}
	return preview, nil
}

// PreviewChange 编辑合集的季绑定时的预览：同 Preview，另外标出季绑定现在绑着的是哪个候选、换成各个候选时会删掉几个绑定，
// 以及条目里处理过的（绑定建在哪一集，不会再动）。不写库。季绑定不存在为 404，文件夹的季绑定为 400；其余错误见 listCandidates。
func (s *Service) PreviewChange(ctx context.Context, id int64, link string, rule source.EpisodeRule) (ChangePreview, error) {
	if _, err := s.getCollection(ctx, id); err != nil {
		return ChangePreview{}, err
	}
	adapter, listed, err := s.listCandidates(ctx, link)
	if err != nil {
		return ChangePreview{}, err
	}
	preview := ChangePreview{Candidates: make([]ChangeCandidate, len(listed))}
	for i, l := range listed {
		if preview.Candidates[i], err = s.changeCandidate(ctx, id, adapter, l, rule); err != nil {
			return ChangePreview{}, err
		}
	}
	return preview, nil
}

// changeCandidate 编辑时预览里的一个候选：比较季绑定现在的合集，查出换过去会删掉的绑定数和处理过的条目。
func (s *Service) changeCandidate(ctx context.Context, id int64, adapter source.Adapter, l listedCandidate, rule source.EpisodeRule) (ChangeCandidate, error) {
	info, err := candidateInfo(adapter, l)
	if err != nil {
		return ChangeCandidate{}, err
	}
	impact, err := s.q.GetCollectionChangeImpact(ctx, seasonbindingdb.GetCollectionChangeImpactParams{
		ID: id, Adapter: adapter.ID(), Ref: l.Ref, Refs: itemRefs(l.col.Items),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChangeCandidate{}, errSeasonBindingNotFound
		}
		return ChangeCandidate{}, fmt.Errorf("get collection change impact of season binding %d: %w", id, err)
	}
	c := ChangeCandidate{CandidateInfo: info, Current: impact.Same}
	if !impact.Same {
		c.RemovedBindings = impact.Outside
	}

	items := source.NumberItems(l.col, rule)
	handled, err := s.q.ListHandledEpisodesByRefs(ctx, seasonbindingdb.ListHandledEpisodesByRefsParams{
		SeasonBindingID: id, Adapter: adapter.ID(), Refs: itemRefs(items),
	})
	if err != nil {
		return ChangeCandidate{}, fmt.Errorf("list handled items of season binding %d: %w", id, err)
	}
	c.Items = make([]ChangeItem, len(items))
	for i, it := range previewItems(items) {
		c.Items[i].PreviewItem = it
	}
	for _, h := range handled {
		c.Items[h.Pos-1].HandledEpisode = new(h.EpisodeNumber)
	}
	return c, nil
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

// candidateInfo 预览里一个候选合集的信息，链接和标签交给适配器生成。
func candidateInfo(adapter source.Adapter, l listedCandidate) (CandidateInfo, error) {
	d, err := adapter.DescribeCollection(l.Ref)
	if err != nil {
		return CandidateInfo{}, fmt.Errorf("describe collection %s: %w", l.Ref, err)
	}
	return CandidateInfo{
		Kind:           l.Kind,
		Title:          l.col.Title,
		SourceURL:      d.URL,
		SourceLabel:    d.Label,
		Finished:       l.col.Finished,
		NumberedByRule: l.col.NumberedByRule,
	}, nil
}

// itemRefs 条目的 ref，转为 JSON 文本，作为查询的数组参数。
func itemRefs(items []source.CollectionItem) []string {
	refs := make([]string, len(items))
	for i, it := range items {
		refs[i] = string(it.Ref)
	}
	return refs
}

// previewItems 认出序号的条目转为预览的条目：认出的带上序号，对不上的带上原因。
func previewItems(items []source.CollectionItem) []PreviewItem {
	out := make([]PreviewItem, len(items))
	for i, it := range items {
		out[i] = PreviewItem{Label: it.Label, Reason: nullIfEmpty(it.Unmatched)}
		if it.Unmatched == "" {
			out[i].Number = new(it.Number)
		}
	}
	return out
}

// checkSeason 确认这一季存在，请求平台之前就能返回 404。
func (s *Service) checkSeason(ctx context.Context, seasonID int64) error {
	switch exists, err := s.q.SeasonExists(ctx, seasonID); {
	case err != nil:
		return fmt.Errorf("check season %d: %w", seasonID, err)
	case !exists:
		return errSeasonNotFound
	}
	return nil
}

// CreateParams 创建季绑定的参数。
type CreateParams struct {
	CollectionChoice
	Mapping source.Mapping
	Rule    source.EpisodeRule // 集号规则；合集不按规则编号时也照样保存，不起作用
}

// CollectionChoice 贴的链接和选的候选：创建季绑定、换合集时按它重新识别、列出合集。
type CollectionChoice struct {
	Link string // 原样保存，编辑时预填
	Kind string // 选哪个候选；链接只有一个候选时可以为空
}

// Create 创建季绑定：重新识别链接、按 kind 选候选、查重（409）、列出合集，保存季绑定（连同贴的链接）和条目，
// 随即在后台补建，返回详情。季不存在为 404，保存前被删除为 404"这一季已被删除"；
// 同时两次绑定同一个合集时，后提交的撞上唯一约束返回 409。其余错误见 resolve。新建的季绑定开着追更。
func (s *Service) Create(ctx context.Context, seasonID int64, p CreateParams) (Detail, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Detail{}, err
	}
	r, err := s.resolve(ctx, seasonID, nil, p.CollectionChoice)
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
			Link:            p.Link,
			Title:           r.col.Title,
			Finished:        r.col.Finished,
			MappingFrom:     int32(p.Mapping.From),
			MappingTo:       int32(p.Mapping.To),
			EpisodePatterns: p.Rule.Patterns(),
			NumberedByRule:  r.col.NumberedByRule,
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errSeasonBindingExists
			}
			return fmt.Errorf("insert season binding of season %d: %w", seasonID, err)
		}
		return saveItems(ctx, q, id, source.NumberItems(r.col, p.Rule))
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

// resolve 创建、换合集时重新识别链接、按 kind 选候选、查重（这一季已经绑定过这个合集时 409，换合集时不算 self 自己），
// 再列出合集；识别与列出共用 source.FetchTimeout。错误同 listCandidates，选不出候选为 400。
func (s *Service) resolve(ctx context.Context, seasonID int64, self *int64, c CollectionChoice) (resolved, error) {
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, c.Link)
	if err != nil {
		return resolved{}, binding.SourceAPIError(err)
	}
	chosen, err := chooseCandidate(candidates, c.Kind)
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

// lockSeason 写入事务里锁住一季到提交（FOR KEY SHARE），期间删不掉它；这一季已被删除时返回 gone。
func lockSeason(ctx context.Context, q *seasonbindingdb.Queries, id int64, gone error) error {
	if _, err := q.LockSeason(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gone
		}
		return fmt.Errorf("lock season %d: %w", id, err)
	}
	return nil
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

// saveItems 在写入事务里保存一次列出的条目：按弹幕源 upsert（位置从 1 开始），删掉合集里已经没有的。
func saveItems(ctx context.Context, q *seasonbindingdb.Queries, id int64, items []source.CollectionItem) error {
	p := seasonbindingdb.UpsertSeasonBindingItemsParams{
		SeasonBindingID: id,
		Refs:            make([]string, len(items)),
		Positions:       make([]int32, len(items)),
		Numbers:         make([]int32, len(items)),
		Reasons:         make([]string, len(items)),
		Labels:          make([]string, len(items)),
	}
	for i, it := range items {
		p.Refs[i] = string(it.Ref)
		p.Positions[i] = int32(i + 1)
		p.Numbers[i] = int32(it.Number)
		if it.Unmatched != "" {
			p.Numbers[i] = -1 // 存为 null
		}
		p.Reasons[i], p.Labels[i] = it.Unmatched, it.Label
	}
	if err := q.DeleteStaleSeasonBindingItems(ctx, seasonbindingdb.DeleteStaleSeasonBindingItemsParams{SeasonBindingID: id, Refs: p.Refs}); err != nil {
		return fmt.Errorf("delete stale items of season binding %d: %w", id, err)
	}
	if err := q.UpsertSeasonBindingItems(ctx, p); err != nil {
		return fmt.Errorf("upsert items of season binding %d: %w", id, err)
	}
	return nil
}

// Get 季绑定的详情，条目表各条目的状态由条目、处理过的记录、绑定和本季的集算出；文件夹的季绑定没有条目表。不存在时返回 404。
func (s *Service) Get(ctx context.Context, id int64) (Detail, error) {
	row, err := s.q.GetSeasonBindingSummary(ctx, seasonbindingdb.GetSeasonBindingSummaryParams{
		ID: id, LeasePrefix: database.LeaseSeasonBackfillPrefix,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, errSeasonBindingNotFound
		}
		return Detail{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	view, err := s.view(row.SeasonBinding, row.BindingCount, row.Running)
	if err != nil {
		return Detail{}, err
	}
	if row.SeasonBinding.Kind == kindFolder {
		return Detail{View: view, Items: []ItemView{}}, nil
	}
	items, err := s.q.ListSeasonBindingItemsWithHandled(ctx, id)
	if err != nil {
		return Detail{}, fmt.Errorf("list items of season binding %d: %w", id, err)
	}
	bound, err := s.q.ListBoundSources(ctx, id)
	if err != nil {
		return Detail{}, fmt.Errorf("list bound sources of season binding %d: %w", id, err)
	}
	numbers, err := s.q.ListEpisodeNumbersBySeason(ctx, row.SeasonBinding.SeasonID)
	if err != nil {
		return Detail{}, fmt.Errorf("list episodes of season %d: %w", row.SeasonBinding.SeasonID, err)
	}
	return Detail{View: view, Items: itemViews(row.SeasonBinding, items, bound, numbers)}, nil
}

// ListBySeries 一部剧的全部季绑定（不含条目表，两种季绑定都在内），按季 ID 分组，组内按创建顺序。剧详情用。
func (s *Service) ListBySeries(ctx context.Context, seriesID int64) (map[int64][]View, error) {
	rows, err := s.q.ListSeasonBindingSummariesBySeries(ctx, seasonbindingdb.ListSeasonBindingSummariesBySeriesParams{
		SeriesID: seriesID, LeasePrefix: database.LeaseSeasonBackfillPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("list season bindings of series %d: %w", seriesID, err)
	}
	views := make(map[int64][]View) // 季 ID → 这一季的季绑定
	for _, row := range rows {
		v, err := s.view(row.SeasonBinding, row.BindingCount, row.Running)
		if err != nil {
			return nil, err
		}
		views[row.SeasonBinding.SeasonID] = append(views[row.SeasonBinding.SeasonID], v)
	}
	return views, nil
}

// sourceAt 一集上的一个弹幕源，ref 是 jsonb 列输出的文本。
type sourceAt struct {
	episode int32
	ref     string
}

// itemViews 算出各条目的状态（见 itemView）。条目与绑定的 ref 都读自 jsonb 列，格式相同，可以直接比较。
func itemViews(sb seasonbindingdb.SeasonBinding, items []seasonbindingdb.ListSeasonBindingItemsWithHandledRow, bound []seasonbindingdb.ListBoundSourcesRow, numbers []int32) []ItemView {
	own := make(map[sourceAt]bool, len(bound)) // 本季现有的绑定是不是这个季绑定建出的
	for _, b := range bound {
		own[sourceAt{b.EpisodeNumber, string(b.Ref)}] = b.Own
	}
	views := make([]ItemView, len(items))
	for i, it := range items {
		views[i] = itemView(sb, it.SeasonBindingItem, it.HandledEpisodeNumber, own, numbers)
	}
	return views
}

// itemView 一个条目的状态。处理过的（builtAt 不为 nil，是绑定建在的那一集）看那一集上这个弹幕源的绑定：
// 是它建出的为已建绑定，别人建的为集上已有，没有了为绑定已被删除。
// 没处理过的依次判断：对不上（含集号重复）、在起点之前、对应的集不存在（等待）、对应的集上已有这个弹幕源、最近一次失败、待补建。
// own 是本季现有的绑定（见 ListBoundSources），numbers 是本季的集号。
func itemView(sb seasonbindingdb.SeasonBinding, it seasonbindingdb.SeasonBindingItem, builtAt *int32, own map[sourceAt]bool, numbers []int32) ItemView {
	v := ItemView{Label: it.Label, Number: it.Number}
	ref := string(it.Ref)
	if builtAt != nil {
		v.EpisodeNumber = builtAt
		switch mine, bound := own[sourceAt{*builtAt, ref}]; {
		case !bound:
			v.State = itemBindingDeleted
		case mine:
			v.State = itemBound
		default:
			v.State = itemAlreadyBound
		}
		return v
	}
	if it.Number == nil {
		v.State, v.Reason = itemUnmatched, it.UnmatchedReason
		return v
	}
	target, ok := mappedEpisode(sb, *it.Number)
	if !ok {
		v.State = itemBeforeStart
		return v
	}
	v.EpisodeNumber = new(target)
	_, bound := own[sourceAt{target, ref}]
	switch {
	case !slices.Contains(numbers, target):
		v.State = itemWaitingEpisode
	case bound:
		v.State = itemAlreadyBound
	case it.LastError != nil:
		v.State, v.Reason, v.LastErrorAt = itemFailed, it.LastError, it.LastErrorAt
	default:
		v.State = itemPending
	}
	return v
}

// mappedEpisode 按合集的季绑定的集号对应，合集序号 number 对到的本地集号。在起点之前时 ok 为 false；
// 对到的集号超出 int 的范围时目录里不可能有这一集，同样不参与。
func mappedEpisode(sb seasonbindingdb.SeasonBinding, number int32) (episode int32, ok bool) {
	n, ok := source.Mapping{From: int(*sb.MappingFrom), To: int(*sb.MappingTo)}.Episode(int(number))
	if !ok || n > math.MaxInt32 {
		return 0, false
	}
	return int32(n), true
}

// view 季绑定的 JSON。合集的季绑定的链接和标签交给它的适配器生成；文件夹的季绑定没有合集，不经过适配器，合集专属的字段为 null。
func (s *Service) view(sb seasonbindingdb.SeasonBinding, bindingCount int32, running bool) (View, error) {
	v := View{
		ID:           sb.ID,
		SeasonID:     sb.SeasonID,
		Kind:         sb.Kind,
		Title:        sb.Title,
		Follow:       sb.Follow,
		Status:       sb.Status,
		Running:      running,
		BindingCount: bindingCount,
		CreatedAt:    sb.CreatedAt,
	}
	if sb.Kind == kindFolder {
		return v, nil
	}
	adapter, err := s.sources.Get(*sb.Adapter)
	if err != nil {
		return View{}, fmt.Errorf("season binding %d: %w", sb.ID, err)
	}
	d, err := adapter.DescribeCollection(sb.Ref)
	if err != nil {
		return View{}, fmt.Errorf("describe season binding %d: %w", sb.ID, err)
	}
	v.Adapter, v.Link = sb.Adapter, sb.Link
	v.SourceURL, v.SourceLabel = &d.URL, &d.Label
	v.Finished = new(sb.Finished)
	v.MappingFrom, v.MappingTo = sb.MappingFrom, sb.MappingTo
	v.NumberedByRule = sb.NumberedByRule
	v.EpisodePatterns = sb.EpisodePatterns
	v.LastError, v.LastCheckedAt = sb.LastError, sb.LastCheckedAt
	return v, nil
}

// UpdateParams 改季绑定的参数，为 nil 的字段不改。
type UpdateParams struct {
	Follow      *bool
	MappingFrom *int32
	MappingTo   *int32
	Rule        *source.EpisodeRule
	// Collection 换合集：按贴的链接和选的候选重新识别、列出合集（见 Update）。选出的就是现在的合集时只改记下的链接
	Collection *CollectionChoice
}

// Update 改集号对应、集号规则，开关追更，换合集。改了集号规则时在同一个事务里按保存的标签重新认出条目的序号（不请求平台）：
// UPDATE 锁住季绑定的行，与补建保存条目的事务前后排队，不会被旧规则认出的序号覆盖。
// 集号对应、集号规则只影响还没处理过的条目。
//
// 换合集先在事务之外识别链接、列出合集（错误见 resolve），再拿住这个季绑定的补建租约直到写完：进行中的补建列的是原来的合集，
// 会把旧的条目写回、建出不在新合集里的绑定，所以正在补建时返回 409。写入事务先锁季、再锁季绑定，
// 换成了另一个合集时删掉它建出的、弹幕源不在新合集里的绑定（连同弹幕）和处理过的记录，留下来的条目照旧算处理过；
// 再写入新的合集、贴的链接，按（改过之后的）集号规则保存新合集的条目。
//
// 打开追更、传了集号对应、集号规则或换了合集时随即在后台补建一次，正在补建时不另起一轮
// （进行中的那一轮处理每个条目之前都重新读季绑定和条目的序号，会用上新的对应和规则）。
// 不存在时返回 404，文件夹的季绑定返回 400。
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) (Detail, error) {
	sb, err := s.getCollection(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	var r resolved
	txCtx := ctx // 换合集时为租约的 ctx，租约丢失时写入随即失败
	var lease *database.Lease
	if p.Collection != nil {
		r, err = s.resolve(ctx, sb.SeasonID, &id, *p.Collection)
		if err != nil {
			return Detail{}, err
		}
		var ok bool
		lease, ok, err = database.TryLease(ctx, s.pool, s.logger, database.LeaseSeasonBackfill(id))
		if err != nil {
			return Detail{}, fmt.Errorf("acquire backfill lease of season binding %d: %w", id, err)
		}
		if !ok {
			return Detail{}, errChangeWhileBackfill
		}
		defer lease.Release() // 出错返回时；写完之后在补建之前就释放
		txCtx = lease.Context()
	}

	params := seasonbindingdb.UpdateSeasonBindingParams{ID: id, Follow: p.Follow, MappingFrom: p.MappingFrom, MappingTo: p.MappingTo}
	if p.Rule != nil {
		params.EpisodePatterns = p.Rule.Patterns()
	}
	var removed int64 // 换合集时删掉的绑定数
	err = pgx.BeginFunc(txCtx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if p.Collection != nil {
			var err error
			if removed, err = changeCollection(txCtx, q, sb, p.Collection.Link, r); err != nil {
				return err
			}
		}
		row, err := q.UpdateSeasonBinding(txCtx, params)
		switch {
		case err != nil:
			return err
		case p.Collection != nil:
			rule, err := source.ParseEpisodeRule(row.EpisodePatterns)
			if err != nil {
				return fmt.Errorf("parse episode patterns of season binding %d: %w", id, err)
			}
			return saveItems(txCtx, q, id, source.NumberItems(r.col, rule))
		case p.Rule != nil && *row.NumberedByRule:
			return renumberItems(txCtx, q, id, *p.Rule)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, errSeasonBindingNotFound
		}
		return Detail{}, fmt.Errorf("update season binding %d: %w", id, err)
	}
	if lease != nil {
		lease.Release() // 随后的补建才拿得到租约
		s.logger.InfoContext(ctx, "season binding collection changed",
			"season_binding_id", id, "adapter", r.adapter.ID(), "ref", string(r.ref), "removed_bindings", removed)
	}
	if p.Follow != nil && *p.Follow || p.MappingFrom != nil || p.MappingTo != nil || p.Rule != nil || p.Collection != nil {
		s.startBackfill(ctx, id)
	}
	return s.Get(ctx, id)
}

// changeCollection 换合集的写入，在 Update 的事务里：先锁季（与补建、删季的加锁顺序一致），再锁季绑定；
// 选出的不是现在的合集时，删掉它建出的、弹幕源不在新合集里的绑定和处理过的记录，返回删掉的绑定数；
// 最后写入新的合集与贴的链接。季（连同季绑定）已被删除时为 404，同一季已经绑定过这个合集时为 409。
func changeCollection(ctx context.Context, q *seasonbindingdb.Queries, sb seasonbindingdb.SeasonBinding, link string, r resolved) (int64, error) {
	if err := lockSeason(ctx, q, sb.SeasonID, errSeasonBindingNotFound); err != nil {
		return 0, err
	}
	same, err := q.LockSeasonBindingCollection(ctx, seasonbindingdb.LockSeasonBindingCollectionParams{
		ID: sb.ID, Adapter: r.adapter.ID(), Ref: r.ref,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errSeasonBindingNotFound
		}
		return 0, fmt.Errorf("lock season binding %d: %w", sb.ID, err)
	}
	var removed int64
	if !same {
		refs := itemRefs(r.col.Items)
		removed, err = q.DeleteBindingsOutsideCollection(ctx, seasonbindingdb.DeleteBindingsOutsideCollectionParams{
			SeasonBindingID: sb.ID, Adapter: r.adapter.ID(), Refs: refs,
		})
		if err != nil {
			return 0, fmt.Errorf("delete bindings outside collection of season binding %d: %w", sb.ID, err)
		}
		err = q.DeleteHandledOutsideCollection(ctx, seasonbindingdb.DeleteHandledOutsideCollectionParams{
			SeasonBindingID: sb.ID, Adapter: r.adapter.ID(), Refs: refs,
		})
		if err != nil {
			return 0, fmt.Errorf("delete handled outside collection of season binding %d: %w", sb.ID, err)
		}
	}
	err = q.SetSeasonBindingCollection(ctx, seasonbindingdb.SetSeasonBindingCollectionParams{
		ID:             sb.ID,
		Adapter:        r.adapter.ID(),
		Ref:            r.ref,
		Link:           link,
		Title:          r.col.Title,
		Finished:       r.col.Finished,
		NumberedByRule: r.col.NumberedByRule,
		Changed:        !same,
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			return 0, errSeasonBindingExists
		}
		return 0, fmt.Errorf("set collection of season binding %d: %w", sb.ID, err)
	}
	return removed, nil
}

// renumberItems 在改集号规则的事务里，按保存的标签用新规则重新认出各条目的序号。
func renumberItems(ctx context.Context, q *seasonbindingdb.Queries, id int64, rule source.EpisodeRule) error {
	rows, err := q.ListSeasonBindingItems(ctx, id)
	if err != nil {
		return fmt.Errorf("list items of season binding %d: %w", id, err)
	}
	col := source.Collection{NumberedByRule: true, Items: make([]source.CollectionItem, len(rows))}
	for i, it := range rows {
		col.Items[i] = source.CollectionItem{Ref: it.Ref, Label: it.Label}
	}
	return saveItems(ctx, q, id, source.NumberItems(col, rule))
}

// Backfill 立即在后台补建一次，追更关着时也能用。不存在时返回 404，文件夹的季绑定返回 400；
// 这个季绑定正在补建时返回 409"正在补建"，不排第二次。
func (s *Service) Backfill(ctx context.Context, id int64) error {
	if err := s.checkCollection(ctx, id); err != nil {
		return err
	}
	return s.trigger(ctx, id)
}

// checkCollection 确认是合集的季绑定，见 getCollection。
func (s *Service) checkCollection(ctx context.Context, id int64) error {
	_, err := s.getCollection(ctx, id)
	return err
}

// getCollection 读合集的季绑定：不存在时为 404，文件夹的季绑定为 400"文件夹的季绑定只能删除"。
// 季绑定的种类和所在的季不会改变，在事务之外判断即可。
func (s *Service) getCollection(ctx context.Context, id int64) (seasonbindingdb.SeasonBinding, error) {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err == nil && sb.Kind != kindCollection {
		return seasonbindingdb.SeasonBinding{}, errFolderDeleteOnly
	}
	return sb, err
}

// Delete 删除季绑定，两种季绑定一样：一个事务里先锁住它（进行中的补建写入事务先提交，之后补建再也锁不到它，随即结束），
// withBindings 时先删它建出的绑定（弹幕、弹幕文件随之级联），再删季绑定；否则它建出的绑定变成普通绑定。不存在时返回 404。
func (s *Service) Delete(ctx context.Context, id int64, withBindings bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockSeasonBindingForDelete(ctx, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingNotFound
			}
			return fmt.Errorf("lock season binding %d: %w", id, err)
		}
		if withBindings {
			if err := q.DeleteBindingsBySeasonBinding(ctx, id); err != nil {
				return fmt.Errorf("delete bindings of season binding %d: %w", id, err)
			}
		}
		if err := q.DeleteSeasonBinding(ctx, id); err != nil {
			return fmt.Errorf("delete season binding %d: %w", id, err)
		}
		return nil
	})
}

// Run 后台循环，阻塞到 ctx 取消；返回前等进行中的扫描和补建停下。只能调用一次。
// 每隔 follow.scan_interval 在后台开始一次追更的扫描（见 scan），启动时不立即扫描；手动触发拿到租约就在后台开始补建，
// 不同季绑定的补建互不等待（对平台的请求由适配器自己限速）。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.follow.ScanInterval)
	defer ticker.Stop()
	s.loop.Run(ctx, ticker.C, func(ctx context.Context) {
		s.loop.Spawn(func() { s.scan(ctx) })
	})
}

// trigger 请 Run 的循环立即在后台补建一次。正在补建时返回 errBackfillRunning；
// Run 已经返回（服务正在关闭）时返回 background.ErrStopped（503）。
func (s *Service) trigger(ctx context.Context, id int64) error {
	return s.loop.Call(ctx, func(ctx context.Context) error { return s.start(ctx, id) })
}

// startBackfill 创建、改集号对应、打开追更之后随即在后台补建一次：正在补建时不另起一轮；服务正在关闭时不补建，
// 追更开着的季绑定重启后由扫描接着做。都不是这次请求的错误，只记日志。
func (s *Service) startBackfill(ctx context.Context, id int64) {
	err := s.trigger(ctx, id)
	if err != nil && !errors.Is(err, errBackfillRunning) && !errors.Is(err, background.ErrStopped) && ctx.Err() == nil {
		s.logger.ErrorContext(ctx, "start backfill failed", "season_binding_id", id, "error", err)
	}
}

// start 拿到按季绑定的租约就在后台补建一轮，租约由补建的 goroutine 持有到结束；拿不到时返回 errBackfillRunning。
func (s *Service) start(ctx context.Context, id int64) error {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSeasonBackfill(id))
	if err != nil {
		return fmt.Errorf("acquire backfill lease of season binding %d: %w", id, err)
	}
	if !ok {
		return errBackfillRunning
	}
	s.loop.Spawn(func() {
		defer lease.Release()
		s.backfill(lease.Context(), id, triggerManual, nil)
	})
	return nil
}

// nullIfEmpty 空串存为 null。
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

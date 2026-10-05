package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errSeasonDeleted         = errcode.ErrNotFound.WithMessage("这一季已被删除")
	errSeasonBindingExists   = errcode.ErrConflict.WithMessage("这一季已经绑定过这个合集")
	errSeasonBindingNotFound = errcode.ErrNotFound.WithMessage("季绑定不存在")
	errBackfillRunning       = errcode.ErrConflict.WithMessage("正在补建")
	errKindRequired          = errcode.ErrBadRequest.WithMessage("链接对应多个合集，请选择一个")
	errKindNotFound          = errcode.ErrBadRequest.WithMessage("链接里没有这种合集，请重新预览")
)

// SeasonBindingService 季绑定：在季上绑定一个合集，按集号对应为各集补建出普通的绑定；追更时定时补建、自动重新拉取。
//
// 生命周期同 SyncService：App.Run 运行 Run(ctx)，补建都用 Run 的 ctx（应用级），不用 HTTP 请求的 ctx；
// 手动触发（创建、立即补建、改集号对应、打开追更）经 Run 的循环立即在后台开始，追更的扫描在 Run 里另一个 goroutine 中
// 每分钟进行一次、一次补建一个。ctx 取消时进行中的补建停下，Run 等它们返回，之后 wire 的 cleanup 才关闭连接池。
//
// "补建中"以按季绑定的 advisory lock（database.LockSeasonBackfill）为准，多实例同样成立，不在表里存运行状态。
// 网络请求都在事务之外；写入事务的第一句锁住季绑定，删除季绑定时排队，之后补建再也锁不到它，随即结束。
type SeasonBindingService struct {
	store    repository.Store
	pool     *pgxpool.Pool // 取专用连接持有按季绑定的锁
	sources  *source.Registry
	bindings *BindingService // 自动重新拉取复用它的 Refetch
	logger   *slog.Logger

	triggers chan backfillTrigger // 手动触发：Run 的循环收到后开始补建，把结果送回
	stopped  chan struct{}        // Run 返回时关闭，之后的手动触发不再等它
	wg       sync.WaitGroup       // 追更的扫描和进行中的补建
}

type backfillTrigger struct {
	id    int64
	reply chan error
}

func NewSeasonBindingService(store repository.Store, pool *pgxpool.Pool, sources *source.Registry, bindings *BindingService, logger *slog.Logger) *SeasonBindingService {
	return &SeasonBindingService{
		store:    store,
		pool:     pool,
		sources:  sources,
		bindings: bindings,
		logger:   logger,
		triggers: make(chan backfillTrigger),
		stopped:  make(chan struct{}),
	}
}

// SeasonBindingView 季绑定的 JSON。不对外暴露原始的合集 ref，sourceUrl / sourceLabel 由适配器的 DescribeCollection 生成。
type SeasonBindingView struct {
	ID            int64      `json:"id"`
	SeasonID      int64      `json:"seasonId"`
	Adapter       string     `json:"adapter"`
	SourceURL     string     `json:"sourceUrl"`
	SourceLabel   string     `json:"sourceLabel"` // 例如"B 站番剧 ss41410"，含合集的种类
	Title         string     `json:"title"`       // 合集标题，每次检查更新
	Finished      bool       `json:"finished"`    // 平台上已完结
	MappingFrom   int32      `json:"mappingFrom"` // 集号对应：合集第 mappingFrom 集为本地第 mappingTo 集
	MappingTo     int32      `json:"mappingTo"`
	Follow        bool       `json:"follow"`
	Status        string     `json:"status"`        // active | dead
	LastError     *string    `json:"lastError"`     // 上次检查结束时的错误
	LastCheckedAt *time.Time `json:"lastCheckedAt"` // 上次检查的开始时间
	Running       bool       `json:"running"`       // 正在补建
	BindingCount  int32      `json:"bindingCount"`  // 它建出的、现存的绑定数
}

// SeasonBindingDetail 季绑定的详情：另有条目表，显示的是上次检查时的合集内容，不实时请求平台。
type SeasonBindingDetail struct {
	SeasonBindingView
	Items []SeasonBindingItemView `json:"items"` // 按在合集里的顺序
}

// 条目的状态，读取时算出，不存储。
const (
	itemBound          = "bound"          // 已建绑定
	itemBindingDeleted = "bindingDeleted" // 处理过，但那一集上已经没有这个弹幕源的绑定（用户删掉了）
	itemUnmatched      = "unmatched"      // 对不上，reason 为原因
	itemBeforeStart    = "beforeStart"    // 序号在集号对应的起点之前
	itemWaitingEpisode = "waitingEpisode" // 目录里还没有对应的集
	itemPending        = "pending"        // 待补建
	itemFailed         = "failed"         // 最近一次补建失败，reason 为原因，下次补建再试
)

// SeasonBindingItemView 条目表的一行。
type SeasonBindingItemView struct {
	Label  string  `json:"label"`
	Note   *string `json:"note"`
	Number *int32  `json:"number"` // 合集序号；对不上时为 null
	State  string  `json:"state"`
	Reason *string `json:"reason"` // 对不上、失败的原因
	// EpisodeNumber 已建绑定、绑定已被删除时为它实际所在的集；其余能算出对应的集时为对应的集号
	EpisodeNumber *int32     `json:"episodeNumber"`
	LastErrorAt   *time.Time `json:"lastErrorAt"` // 失败时为失败的时间
}

// CollectionPreview 预览：链接识别出的各个候选合集，不保存任何东西。
type CollectionPreview struct {
	Candidates []PreviewCandidate `json:"candidates"`
}

type PreviewCandidate struct {
	Kind           string        `json:"kind"` // 创建时传回，用来选候选
	Title          string        `json:"title"`
	SourceURL      string        `json:"sourceUrl"`
	SourceLabel    string        `json:"sourceLabel"`
	Finished       bool          `json:"finished"`
	DefaultMapping MappingView   `json:"defaultMapping"`
	Items          []PreviewItem `json:"items"`
}

type MappingView struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// PreviewItem 预览的条目；重复的序号已标为对不上（number 为 null、unmatchedReason 为"集号重复"）。
type PreviewItem struct {
	Label           string  `json:"label"`
	Note            *string `json:"note"`
	Number          *int    `json:"number"`
	UnmatchedReason *string `json:"unmatchedReason"`
}

// Preview 识别链接、列出每个候选合集，给出默认的集号对应。不写库。识别与列出共用 fetchTimeout。
// 季不存在为 404；链接无法识别、不能作为合集绑定为 400；合集不存在为 422；上游故障、限流、超时为 502。
func (s *SeasonBindingService) Preview(ctx context.Context, seasonID int64, link string) (CollectionPreview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return CollectionPreview{}, err
	}
	numbers, err := s.store.ListEpisodeNumbersBySeason(ctx, seasonID)
	if err != nil {
		return CollectionPreview{}, fmt.Errorf("list episodes of season %d: %w", seasonID, err)
	}
	episodes := make([]int, len(numbers))
	for i, n := range numbers {
		episodes[i] = int(n)
	}
	netCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, link)
	if err != nil {
		return CollectionPreview{}, sourceError(err)
	}
	collector := adapter.(source.Collector) // ParseCollectionLink 只交给实现了 Collector 的适配器

	preview := CollectionPreview{Candidates: make([]PreviewCandidate, 0, len(candidates))}
	for _, c := range candidates {
		col, err := collector.ListCollection(netCtx, c.Ref)
		if err != nil {
			return CollectionPreview{}, sourceError(err)
		}
		d, err := collector.DescribeCollection(c.Ref)
		if err != nil {
			return CollectionPreview{}, fmt.Errorf("describe collection %s: %w", c.Ref, err)
		}
		items := normalizeItems(col.Items)
		m := source.DefaultMapping(items, episodes)
		pc := PreviewCandidate{
			Kind:           c.Kind,
			Title:          col.Title,
			SourceURL:      d.URL,
			SourceLabel:    d.Label,
			Finished:       col.Finished,
			DefaultMapping: MappingView{From: m.From, To: m.To},
			Items:          make([]PreviewItem, len(items)),
		}
		for i, it := range items {
			pi := PreviewItem{Label: it.Label, Note: nullIfEmpty(it.Note), UnmatchedReason: nullIfEmpty(it.Unmatched)}
			if it.Unmatched == "" {
				pi.Number = new(it.Number)
			}
			pc.Items[i] = pi
		}
		preview.Candidates = append(preview.Candidates, pc)
	}
	return preview, nil
}

// checkSeason 确认这一季存在，请求平台之前就能返回 404。
func (s *SeasonBindingService) checkSeason(ctx context.Context, seasonID int64) error {
	switch exists, err := s.store.SeasonExists(ctx, seasonID); {
	case err != nil:
		return fmt.Errorf("check season %d: %w", seasonID, err)
	case !exists:
		return errSeasonNotFound
	}
	return nil
}

// CreateSeasonBinding 创建季绑定的参数。
type CreateSeasonBinding struct {
	Link    string
	Kind    string // 选哪个候选；链接只有一个候选时可以为空
	Mapping source.Mapping
}

// Create 创建季绑定：重新识别链接、按 kind 选候选、查重（409）、列出合集，保存季绑定和条目，随即在后台补建，返回详情。
// 季不存在为 404，保存前被删除为 404"这一季已被删除"；同时两次绑定同一个合集时，后提交的撞上唯一约束返回 409。
// 其余错误同 Preview。新建的季绑定开着追更。
func (s *SeasonBindingService) Create(ctx context.Context, seasonID int64, p CreateSeasonBinding) (SeasonBindingDetail, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return SeasonBindingDetail{}, err
	}
	netCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, p.Link)
	if err != nil {
		return SeasonBindingDetail{}, sourceError(err)
	}
	c, err := chooseCandidate(candidates, p.Kind)
	if err != nil {
		return SeasonBindingDetail{}, err
	}
	// 只是省掉一次注定 409 的列出；并发时以写入事务里的唯一约束为准
	key := repository.SeasonBindingExistsParams{SeasonID: seasonID, Adapter: adapter.ID(), Ref: c.Ref}
	switch exists, err := s.store.SeasonBindingExists(ctx, key); {
	case err != nil:
		return SeasonBindingDetail{}, fmt.Errorf("check season binding of season %d: %w", seasonID, err)
	case exists:
		return SeasonBindingDetail{}, errSeasonBindingExists
	}
	col, err := adapter.(source.Collector).ListCollection(netCtx, c.Ref)
	if err != nil {
		return SeasonBindingDetail{}, sourceError(err)
	}

	var id int64
	err = s.store.ExecTx(ctx, func(q repository.Querier) error {
		// 锁住这一季到提交：之后的删除要等这个事务提交，再连同季绑定一起删掉
		if _, err := q.LockSeason(ctx, seasonID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonDeleted
			}
			return fmt.Errorf("lock season %d: %w", seasonID, err)
		}
		var err error
		id, err = q.InsertSeasonBinding(ctx, repository.InsertSeasonBindingParams{
			SeasonID:    seasonID,
			Adapter:     adapter.ID(),
			Ref:         c.Ref,
			Title:       col.Title,
			Finished:    col.Finished,
			MappingFrom: int32(p.Mapping.From),
			MappingTo:   int32(p.Mapping.To),
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errSeasonBindingExists
			}
			return fmt.Errorf("insert season binding of season %d: %w", seasonID, err)
		}
		return saveItems(ctx, q, id, normalizeItems(col.Items))
	})
	if err != nil {
		return SeasonBindingDetail{}, err
	}
	s.startBackfill(ctx, id)
	return s.Get(ctx, id)
}

// chooseCandidate 按 kind 选候选：只有一个候选时 kind 可以为空；有多个候选而没给 kind、或 kind 不在候选里时为 400。
func chooseCandidate(candidates []source.Candidate, kind string) (source.Candidate, error) {
	if kind == "" {
		if len(candidates) != 1 {
			return source.Candidate{}, errKindRequired
		}
		return candidates[0], nil
	}
	i := slices.IndexFunc(candidates, func(c source.Candidate) bool { return c.Kind == kind })
	if i < 0 {
		return source.Candidate{}, errKindNotFound
	}
	return candidates[i], nil
}

// normalizeItems 去掉 ref 重复的条目（保留第一个），再标出重复的序号。
func normalizeItems(items []source.CollectionItem) []source.CollectionItem {
	seen := make(map[string]bool, len(items))
	unique := make([]source.CollectionItem, 0, len(items))
	for _, it := range items {
		if !seen[string(it.Ref)] {
			seen[string(it.Ref)] = true
			unique = append(unique, it)
		}
	}
	return source.MarkDuplicateNumbers(unique)
}

// saveItems 在写入事务里保存一次列出的条目：按弹幕源 upsert（位置从 1 开始），删掉合集里已经没有的。
func saveItems(ctx context.Context, q repository.Querier, id int64, items []source.CollectionItem) error {
	p := repository.UpsertSeasonBindingItemsParams{
		SeasonBindingID: id,
		Refs:            make([]string, len(items)),
		Positions:       make([]int32, len(items)),
		Numbers:         make([]int32, len(items)),
		Reasons:         make([]string, len(items)),
		Labels:          make([]string, len(items)),
		Notes:           make([]string, len(items)),
	}
	for i, it := range items {
		p.Refs[i] = string(it.Ref)
		p.Positions[i] = int32(i + 1)
		p.Numbers[i] = int32(it.Number)
		if it.Unmatched != "" {
			p.Numbers[i] = -1 // 存为 null
		}
		p.Reasons[i], p.Labels[i], p.Notes[i] = it.Unmatched, it.Label, it.Note
	}
	if err := q.DeleteStaleSeasonBindingItems(ctx, repository.DeleteStaleSeasonBindingItemsParams{SeasonBindingID: id, Refs: p.Refs}); err != nil {
		return fmt.Errorf("delete stale items of season binding %d: %w", id, err)
	}
	if err := q.UpsertSeasonBindingItems(ctx, p); err != nil {
		return fmt.Errorf("upsert items of season binding %d: %w", id, err)
	}
	return nil
}

// Get 季绑定的详情，条目表各条目的状态由条目、处理过的记录、绑定和本季的集算出。不存在时返回 404。
func (s *SeasonBindingService) Get(ctx context.Context, id int64) (SeasonBindingDetail, error) {
	row, err := s.store.GetSeasonBindingSummary(ctx, repository.GetSeasonBindingSummaryParams{
		ID: id, LockNamespace: database.LockSeasonBackfill,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SeasonBindingDetail{}, errSeasonBindingNotFound
		}
		return SeasonBindingDetail{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	view, err := seasonBindingView(s.sources, row.SeasonBinding, row.BindingCount, row.Running)
	if err != nil {
		return SeasonBindingDetail{}, err
	}
	items, err := s.store.ListSeasonBindingItems(ctx, id)
	if err != nil {
		return SeasonBindingDetail{}, fmt.Errorf("list items of season binding %d: %w", id, err)
	}
	handled, err := s.store.ListSeasonBindingHandled(ctx, id)
	if err != nil {
		return SeasonBindingDetail{}, fmt.Errorf("list handled of season binding %d: %w", id, err)
	}
	numbers, err := s.store.ListEpisodeNumbersBySeason(ctx, row.SeasonBinding.SeasonID)
	if err != nil {
		return SeasonBindingDetail{}, fmt.Errorf("list episodes of season %d: %w", row.SeasonBinding.SeasonID, err)
	}
	return SeasonBindingDetail{SeasonBindingView: view, Items: itemViews(row.SeasonBinding, items, handled, numbers)}, nil
}

// itemViews 算出各条目的状态。处理过的：那一集上还有这个弹幕源的绑定为已建绑定，否则为绑定已被删除。
// 没处理过的依次判断：对不上（含集号重复）、在起点之前、对应的集不存在（等待）、最近一次失败、待补建。
// 条目与处理过的记录的 ref 都读自 jsonb 列，格式相同，可以直接比较。
func itemViews(sb repository.SeasonBinding, items []repository.SeasonBindingItem, handled []repository.ListSeasonBindingHandledRow, numbers []int32) []SeasonBindingItemView {
	done := make(map[string]repository.ListSeasonBindingHandledRow, len(handled))
	for _, h := range handled {
		done[string(h.Ref)] = h
	}
	views := make([]SeasonBindingItemView, len(items))
	for i, it := range items {
		v := SeasonBindingItemView{Label: it.Label, Note: it.Note, Number: it.Number}
		if h, ok := done[string(it.Ref)]; ok {
			v.EpisodeNumber = new(h.EpisodeNumber)
			v.State = itemBindingDeleted
			if h.Bound {
				v.State = itemBound
			}
			views[i] = v
			continue
		}
		if it.Number == nil {
			v.State, v.Reason = itemUnmatched, it.UnmatchedReason
			views[i] = v
			continue
		}
		target, ok := mappedEpisode(sb, *it.Number)
		switch {
		case !ok:
			v.State = itemBeforeStart
		case !slices.Contains(numbers, target):
			v.State = itemWaitingEpisode
		case it.LastError != nil:
			v.State, v.Reason, v.LastErrorAt = itemFailed, it.LastError, it.LastErrorAt
		default:
			v.State = itemPending
		}
		if ok {
			v.EpisodeNumber = new(target)
		}
		views[i] = v
	}
	return views
}

// mappedEpisode 按季绑定的集号对应，合集序号 number 对到的本地集号。在起点之前时 ok 为 false；
// 对到的集号超出 int 的范围时目录里不可能有这一集，同样不参与。
func mappedEpisode(sb repository.SeasonBinding, number int32) (episode int32, ok bool) {
	n, ok := source.Mapping{From: int(sb.MappingFrom), To: int(sb.MappingTo)}.Episode(int(number))
	if !ok || n > math.MaxInt32 {
		return 0, false
	}
	return int32(n), true
}

// seasonBindingView 季绑定的 JSON，合集的链接和标签交给它的适配器生成。
func seasonBindingView(sources *source.Registry, sb repository.SeasonBinding, bindingCount int32, running bool) (SeasonBindingView, error) {
	adapter, err := sources.Get(sb.Adapter)
	if err != nil {
		return SeasonBindingView{}, fmt.Errorf("season binding %d: %w", sb.ID, err)
	}
	collector, err := collectorOf(adapter)
	if err != nil {
		return SeasonBindingView{}, fmt.Errorf("season binding %d: %w", sb.ID, err)
	}
	d, err := collector.DescribeCollection(sb.Ref)
	if err != nil {
		return SeasonBindingView{}, fmt.Errorf("describe season binding %d: %w", sb.ID, err)
	}
	return SeasonBindingView{
		ID:            sb.ID,
		SeasonID:      sb.SeasonID,
		Adapter:       sb.Adapter,
		SourceURL:     d.URL,
		SourceLabel:   d.Label,
		Title:         sb.Title,
		Finished:      sb.Finished,
		MappingFrom:   sb.MappingFrom,
		MappingTo:     sb.MappingTo,
		Follow:        sb.Follow,
		Status:        sb.Status,
		LastError:     sb.LastError,
		LastCheckedAt: sb.LastCheckedAt,
		Running:       running,
		BindingCount:  bindingCount,
	}, nil
}

// collectorOf 季绑定的适配器的合集能力；没有时按服务器内部错误处理。
func collectorOf(a source.Adapter) (source.Collector, error) {
	c, ok := a.(source.Collector)
	if !ok {
		return nil, fmt.Errorf("source: adapter %q has no collections", a.ID())
	}
	return c, nil
}

// UpdateSeasonBinding 改季绑定的参数，为 nil 的字段不改。
type UpdateSeasonBinding struct {
	Follow      *bool
	MappingFrom *int32
	MappingTo   *int32
}

// Update 改集号对应、开关追更：单条 UPDATE，不锁其他行。打开追更或传了集号对应时随即在后台补建一次，
// 正在补建时不另起一轮（进行中的那一轮处理每个条目之前都重新读季绑定，会用上新的对应）。不存在时返回 404。
func (s *SeasonBindingService) Update(ctx context.Context, id int64, p UpdateSeasonBinding) (SeasonBindingDetail, error) {
	_, err := s.store.UpdateSeasonBinding(ctx, repository.UpdateSeasonBindingParams{
		ID: id, Follow: p.Follow, MappingFrom: p.MappingFrom, MappingTo: p.MappingTo,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SeasonBindingDetail{}, errSeasonBindingNotFound
		}
		return SeasonBindingDetail{}, fmt.Errorf("update season binding %d: %w", id, err)
	}
	if p.Follow != nil && *p.Follow || p.MappingFrom != nil || p.MappingTo != nil {
		s.startBackfill(ctx, id)
	}
	return s.Get(ctx, id)
}

// Backfill 立即在后台补建一次，追更关着时也能用。不存在时返回 404；这个季绑定正在补建时返回 409"正在补建"，不排第二次。
// 另一个季绑定的手动补建在进行时排队，返回 nil，前一轮结束后开始（见 Run）。
func (s *SeasonBindingService) Backfill(ctx context.Context, id int64) error {
	if _, err := s.store.GetSeasonBinding(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errSeasonBindingNotFound
		}
		return fmt.Errorf("get season binding %d: %w", id, err)
	}
	return s.trigger(ctx, id)
}

// Delete 删除季绑定：一个事务里先锁住它（进行中的补建写入事务先提交，之后补建再也锁不到它，随即结束），
// withBindings 时先删它建出的绑定（弹幕随之级联），再删季绑定；否则它建出的绑定变成普通绑定。不存在时返回 404。
func (s *SeasonBindingService) Delete(ctx context.Context, id int64, withBindings bool) error {
	return s.store.ExecTx(ctx, func(q repository.Querier) error {
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

// Run 后台循环，阻塞到 ctx 取消；返回前等进行中的补建停下。只能调用一次。
// 追更的扫描在另一个 goroutine 里每分钟进行一次，启动时不立即扫描；这里处理手动触发：同一时间只进行一轮手动补建，
// 多出来的按触发的先后排队，同一个季绑定只排一次。
//
// 每轮补建用一个专用连接持有它的锁，轮内的查询另外取连接。同步、追更的扫描、手动补建同时进行时，持锁的连接最多三个，
// 连接池至少有四个连接（见 config 的 database.max_conns），轮内的查询总有连接可用，不会互相等死。
func (s *SeasonBindingService) Run(ctx context.Context) {
	defer close(s.stopped)
	s.wg.Go(func() { s.follow(ctx) })
	var (
		queue []int64                  // 排队的手动补建，按触发的先后
		busy  bool                     // 有一轮手动补建在进行
		done  = make(chan struct{}, 1) // 手动补建的一轮结束；同一时间只有一轮，有一个缓冲就不会挡住关闭
	)
	next := func() {
		for !busy && len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			err := s.start(ctx, id, done)
			busy = err == nil
			if err != nil && !errors.Is(err, errBackfillRunning) && ctx.Err() == nil {
				s.logger.ErrorContext(ctx, "start queued backfill failed", "season_binding_id", id, "error", err)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case t := <-s.triggers:
			var err error
			switch {
			case !busy: // 这时队列一定是空的
				err = s.start(ctx, t.id, done)
				busy = err == nil
			case slices.Contains(queue, t.id):
			default:
				if err = s.checkIdle(ctx, t.id); err == nil {
					queue = append(queue, t.id)
				}
			}
			t.reply <- err
		case <-done:
			busy = false
			next()
		}
	}
}

// trigger 请 Run 的循环立即在后台补建一次（有一轮手动补建在进行时排队）。正在补建时返回 errBackfillRunning；
// Run 已经返回（服务正在关闭）时返回 503。
func (s *SeasonBindingService) trigger(ctx context.Context, id int64) error {
	t := backfillTrigger{id: id, reply: make(chan error, 1)}
	select {
	case s.triggers <- t:
	case <-s.stopped:
		return errShuttingDown
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-t.reply
}

// startBackfill 创建、改集号对应、打开追更之后随即在后台补建一次：正在补建时不另起一轮；服务正在关闭时不补建，
// 追更开着的季绑定重启后由扫描接着做。都不是这次请求的错误，只记日志。
func (s *SeasonBindingService) startBackfill(ctx context.Context, id int64) {
	err := s.trigger(ctx, id)
	if err != nil && !errors.Is(err, errBackfillRunning) && !errors.Is(err, errShuttingDown) && ctx.Err() == nil {
		s.logger.ErrorContext(ctx, "start backfill failed", "season_binding_id", id, "error", err)
	}
}

// start 拿到按季绑定的锁就在后台补建一轮，锁由补建的 goroutine 持有到结束，结束后在 done 上报到；
// 拿不到时返回 errBackfillRunning。
func (s *SeasonBindingService) start(ctx context.Context, id int64, done chan<- struct{}) error {
	unlock, ok, err := database.TryAdvisoryLockPair(ctx, s.pool, database.LockSeasonBackfill, id)
	if err != nil {
		return fmt.Errorf("acquire backfill lock of season binding %d: %w", id, err)
	}
	if !ok {
		return errBackfillRunning
	}
	s.wg.Go(func() {
		defer func() {
			unlock()
			done <- struct{}{}
		}()
		s.backfill(ctx, id)
	})
	return nil
}

// checkIdle 这个季绑定没有在补建（包括其他实例、追更的扫描）时返回 nil，否则返回 errBackfillRunning。只试一下锁，不持有。
func (s *SeasonBindingService) checkIdle(ctx context.Context, id int64) error {
	unlock, ok, err := database.TryAdvisoryLockPair(ctx, s.pool, database.LockSeasonBackfill, id)
	if err != nil {
		return fmt.Errorf("acquire backfill lock of season binding %d: %w", id, err)
	}
	if !ok {
		return errBackfillRunning
	}
	unlock()
	return nil
}

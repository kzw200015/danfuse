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
// 按季上传留下的文件夹的季绑定也由它管理：只能查看和删除，补建、追更、改集号对应和集号规则都只适用于合集的季绑定。
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

	Adapter     *string `json:"adapter"`
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
	Kind        string `json:"kind"` // 创建时传回，用来选候选
	Title       string `json:"title"`
	SourceURL   string `json:"sourceUrl"`
	SourceLabel string `json:"sourceLabel"`
	Finished    bool   `json:"finished"`
	// NumberedByRule 序号由集号规则从条目的标签认出：预览按请求里的集号规则认，创建时传同一个规则
	NumberedByRule bool          `json:"numberedByRule"`
	Items          []PreviewItem `json:"items"`
}

// PreviewItem 预览的条目，字段与季绑定的条目相同；重复的序号已标为对不上（number 为 null、reason 为"集号重复"）。
type PreviewItem struct {
	Label  string  `json:"label"`
	Number *int    `json:"number"`
	Reason *string `json:"reason"` // 对不上的原因
}

// Preview 识别链接、列出每个候选合集，按集号规则认出按规则编号的合集的序号。不写库。识别与列出共用 source.FetchTimeout。
// 季不存在为 404；链接无法识别、不能作为合集绑定为 400；合集不存在为 422；上游故障、限流、超时为 502。
func (s *Service) Preview(ctx context.Context, seasonID int64, link string, rule source.EpisodeRule) (CollectionPreview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return CollectionPreview{}, err
	}
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, link)
	if err != nil {
		return CollectionPreview{}, binding.SourceAPIError(err)
	}

	preview := CollectionPreview{Candidates: make([]PreviewCandidate, 0, len(candidates))}
	for _, c := range candidates {
		col, err := adapter.ListCollection(netCtx, c.Ref)
		if err != nil {
			return CollectionPreview{}, binding.SourceAPIError(err)
		}
		d, err := adapter.DescribeCollection(c.Ref)
		if err != nil {
			return CollectionPreview{}, fmt.Errorf("describe collection %s: %w", c.Ref, err)
		}
		items := source.NumberItems(col, rule)
		pc := PreviewCandidate{
			Kind:           c.Kind,
			Title:          col.Title,
			SourceURL:      d.URL,
			SourceLabel:    d.Label,
			Finished:       col.Finished,
			NumberedByRule: col.NumberedByRule,
			Items:          make([]PreviewItem, len(items)),
		}
		for i, it := range items {
			pi := PreviewItem{Label: it.Label, Reason: nullIfEmpty(it.Unmatched)}
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
	Link    string
	Kind    string // 选哪个候选；链接只有一个候选时可以为空
	Mapping source.Mapping
	Rule    source.EpisodeRule // 集号规则；合集不按规则编号时也照样保存，不起作用
}

// Create 创建季绑定：重新识别链接、按 kind 选候选、查重（409）、列出合集，保存季绑定和条目，随即在后台补建，返回详情。
// 季不存在为 404，保存前被删除为 404"这一季已被删除"；同时两次绑定同一个合集时，后提交的撞上唯一约束返回 409。
// 其余错误同 Preview。新建的季绑定开着追更。
func (s *Service) Create(ctx context.Context, seasonID int64, p CreateParams) (Detail, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Detail{}, err
	}
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, candidates, err := s.sources.ParseCollectionLink(netCtx, p.Link)
	if err != nil {
		return Detail{}, binding.SourceAPIError(err)
	}
	c, err := chooseCandidate(candidates, p.Kind)
	if err != nil {
		return Detail{}, err
	}
	// 只是省掉一次注定 409 的列出；并发时以写入事务里的唯一约束为准
	key := seasonbindingdb.SeasonBindingExistsParams{SeasonID: seasonID, Adapter: adapter.ID(), Ref: c.Ref}
	switch exists, err := s.q.SeasonBindingExists(ctx, key); {
	case err != nil:
		return Detail{}, fmt.Errorf("check season binding of season %d: %w", seasonID, err)
	case exists:
		return Detail{}, errSeasonBindingExists
	}
	col, err := adapter.ListCollection(netCtx, c.Ref)
	if err != nil {
		return Detail{}, binding.SourceAPIError(err)
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
			Adapter:         adapter.ID(),
			Ref:             c.Ref,
			Title:           col.Title,
			Finished:        col.Finished,
			MappingFrom:     int32(p.Mapping.From),
			MappingTo:       int32(p.Mapping.To),
			EpisodePatterns: p.Rule.Patterns(),
			NumberedByRule:  col.NumberedByRule,
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errSeasonBindingExists
			}
			return fmt.Errorf("insert season binding of season %d: %w", seasonID, err)
		}
		return saveItems(ctx, q, id, source.NumberItems(col, p.Rule))
	})
	if err != nil {
		return Detail{}, err
	}
	s.startBackfill(ctx, id)
	return s.Get(ctx, id)
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
	v.Adapter = sb.Adapter
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
}

// Update 改集号对应、集号规则，开关追更。改了集号规则时在同一个事务里按保存的标签重新认出条目的序号（不请求平台）：
// UPDATE 锁住季绑定的行，与补建保存条目的事务前后排队，不会被旧规则认出的序号覆盖。
// 打开追更、传了集号对应或集号规则时随即在后台补建一次，正在补建时不另起一轮
// （进行中的那一轮处理每个条目之前都重新读季绑定和条目的序号，会用上新的对应和规则）。
// 不存在时返回 404，文件夹的季绑定返回 400。
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) (Detail, error) {
	if err := s.checkCollection(ctx, id); err != nil {
		return Detail{}, err
	}
	params := seasonbindingdb.UpdateSeasonBindingParams{ID: id, Follow: p.Follow, MappingFrom: p.MappingFrom, MappingTo: p.MappingTo}
	if p.Rule != nil {
		params.EpisodePatterns = p.Rule.Patterns()
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		byRule, err := q.UpdateSeasonBinding(ctx, params)
		if err != nil || p.Rule == nil || !*byRule {
			return err
		}
		return renumberItems(ctx, q, id, *p.Rule)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, errSeasonBindingNotFound
		}
		return Detail{}, fmt.Errorf("update season binding %d: %w", id, err)
	}
	if p.Follow != nil && *p.Follow || p.MappingFrom != nil || p.MappingTo != nil || p.Rule != nil {
		s.startBackfill(ctx, id)
	}
	return s.Get(ctx, id)
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

// checkCollection 确认是合集的季绑定：不存在时为 404，文件夹的季绑定为 400"文件夹的季绑定只能删除"。
// 季绑定的种类不会改变，在事务之外判断即可。
func (s *Service) checkCollection(ctx context.Context, id int64) error {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err == nil && sb.Kind != kindCollection {
		return errFolderDeleteOnly
	}
	return err
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

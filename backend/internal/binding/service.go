// Package binding 绑定：一集与一个弹幕源的对应关系。贴链接创建（拉取弹幕源的全部弹幕落库）、重新拉取、改偏移与删除，
// 用弹幕文件建的绑定（file.go），查看保存的弹幕和读取一集合并后的弹幕（danmaku.go），以及定时拉取（scheduled_fetch.go）。
// 季绑定补建出的绑定也由这里写入（CreateBackfilledInTx）。
package binding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding/bindingdb"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errEpisodeNotFound = apierr.ErrNotFound.WithMessage("集不存在")
	errEpisodeDeleted  = apierr.ErrNotFound.WithMessage("这一集已被删除")
	errBindingExists   = apierr.ErrConflict.WithMessage("这一集已经绑定过这个弹幕源")
	errBindingNotFound = apierr.ErrNotFound.WithMessage("绑定不存在")
	errBindingDeleted  = apierr.ErrNotFound.WithMessage("绑定已被删除")
	errNotLinkBinding  = apierr.ErrBadRequest.WithMessage("用弹幕文件建的绑定不能重新拉取")
)

// 绑定的 kind：弹幕源的形态（见 docs/adr/0004）。
const (
	kindLink = "link" // 贴链接或补建出的，按适配器和 ref 能重新拉取
	kindFile = "file" // 上传的一组弹幕文件，没有适配器和 ref
)

// Service 绑定：贴链接创建，拉取弹幕源的全部弹幕落库；重新拉取、改偏移与删除。
// 用弹幕文件建的绑定（创建、追加文件、重新解析）见 file.go。
// 拉取（网络请求）都在事务之外，拉完才开写入事务，写入事务的第一句锁住要写的行（创建时锁集，重新拉取时锁绑定）；
// 不加应用层的锁，并发靠行锁、外键级联和唯一约束。
type Service struct {
	pool    *pgxpool.Pool
	q       *bindingdb.Queries
	sources *source.Registry
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: bindingdb.New(pool), sources: sources, logger: logger}
}

// View 绑定的 JSON。不对外暴露原始 ref。sourceUrl / sourceLabel：贴链接建的由适配器的
// Describe 生成；用弹幕文件建的没有链接，标签写明文件的份数。
type View struct {
	ID           int64   `json:"id"`
	Kind         string  `json:"kind"`        // link | file
	Adapter      *string `json:"adapter"`     // 用弹幕文件建的为 null
	SourceURL    *string `json:"sourceUrl"`   // 用弹幕文件建的为 null
	SourceLabel  string  `json:"sourceLabel"` // 例如"B 站投稿 BV1xx411c7XX P2"、"弹幕文件 · 5 份"
	Title        string  `json:"title"`       // 弹幕源的标题
	Duration     *int32  `json:"duration"`    // 弹幕源视频的时长，秒；用弹幕文件建的没有时长，为 null
	Offset       float64 `json:"offset"`      // 秒，正数表示弹幕延后
	Status       string  `json:"status"`      // active | dead
	DanmakuCount int32   `json:"danmakuCount"`
	// ContentVersion 弹幕内容的版本：插入了新弹幕、或替换了全部弹幕时加 1，改偏移不变。管理界面据此刷新已打开的弹幕列表
	ContentVersion int32 `json:"contentVersion"`
	// MaxTimeMs 最晚一条弹幕的时间（毫秒，未校正），管理界面查看弹幕时作为拖动条的长度；没有弹幕时为 0
	MaxTimeMs     int32      `json:"maxTimeMs"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	// SeasonBindingID 建出这个绑定的季绑定；手动贴链接建的、或季绑定已被删除的为 null
	SeasonBindingID *int64 `json:"seasonBindingId"`
}

// SourceAPIError 把适配器的 *source.Error 转成管理 API 的错误：InvalidLink 为 400，NotFound 为 422，其余为 502；
// 提示用适配器写的 Message，底层原因 Err 只进日志（不再包一层 *source.Error，日志里提示不重复）。
// 其他错误原样返回，按服务器内部错误处理。转换后取不出 Kind，要按 Kind 分支的调用方（补建、定时拉取）用转换之前的错误。
// 季绑定的预览与创建也用它。
func SourceAPIError(err error) error {
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		return err
	}
	base := apierr.ErrBadGateway
	switch srcErr.Kind {
	case source.InvalidLink:
		base = apierr.ErrBadRequest
	case source.NotFound:
		base = apierr.ErrUnprocessable
	}
	return base.WithMessage(srcErr.Message).Wrap(srcErr.Err)
}

// fetch 拉取一个弹幕源的全部弹幕，总时限 source.FetchTimeout。调用方拉完才开写入事务。
func fetch(ctx context.Context, adapter source.Adapter, ref source.Ref) (source.Fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	return adapter.Fetch(ctx, ref)
}

// Create 贴链接创建绑定：解析链接 → 确认这一集存在（404）→ 查重（409）→ 拉取 → 写入。拉取失败就不创建。
// 拉取期间这一集被删除时返回 404"这一集已被删除"；同时两次给同一集贴同一个弹幕源时，后提交的撞上唯一约束返回 409。
func (s *Service) Create(ctx context.Context, episodeID int64, link string) (View, error) {
	// 解析短链也要联网：解析链接与拉取共用 source.FetchTimeout，从解析开始计时；查库和写入事务仍用 ctx
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, ref, err := s.sources.ParseLink(netCtx, link)
	if err != nil {
		return View{}, SourceAPIError(err)
	}
	switch exists, err := s.q.EpisodeExists(ctx, episodeID); {
	case err != nil:
		return View{}, fmt.Errorf("check episode %d: %w", episodeID, err)
	case !exists:
		return View{}, errEpisodeNotFound
	}
	// 只是省掉一次注定 409 的拉取；并发时以写入事务里的唯一约束为准
	switch bound, err := s.Exists(ctx, episodeID, adapter.ID(), ref); {
	case err != nil:
		return View{}, err
	case bound:
		return View{}, errBindingExists
	}

	fetched, err := fetch(netCtx, adapter, ref)
	if err != nil {
		return View{}, SourceAPIError(err)
	}
	fetchedAt := time.Now()

	var (
		binding bindingdb.Binding
		added   int64
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 锁住这一集到提交：之后的删除要等这个事务提交，再连同绑定和弹幕一起删掉
		if err := lockEpisode(ctx, q, episodeID, errEpisodeDeleted); err != nil {
			return err
		}
		id, err := q.InsertBinding(ctx, bindingdb.InsertBindingParams{
			EpisodeID: episodeID,
			Adapter:   adapter.ID(),
			Ref:       ref,
			Title:     fetched.Title,
			Duration:  int32(fetched.Duration),
			CreatedAt: fetchedAt,
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errBindingExists
			}
			return fmt.Errorf("insert binding of episode %d: %w", episodeID, err)
		}
		binding, added, err = saveFetched(ctx, q, id, fetched, false, fetchedAt)
		return err
	})
	if err != nil {
		return View{}, err
	}
	s.logFetched(ctx, binding, fetched, added)
	return s.view(binding)
}

// Exists 这一集上是否已有这个弹幕源的绑定。只用来省掉一次注定撞上唯一约束的拉取，并发时以唯一约束为准；
// 季绑定补建之前也用它跳过已绑定的条目。
func (s *Service) Exists(ctx context.Context, episodeID int64, adapter string, ref []byte) (bool, error) {
	bound, err := s.q.BindingExists(ctx, bindingdb.BindingExistsParams{EpisodeID: episodeID, Adapter: adapter, Ref: ref})
	if err != nil {
		return false, fmt.Errorf("check binding of episode %d: %w", episodeID, err)
	}
	return bound, nil
}

// ListBySeries 一部剧的全部绑定，按集 ID 分组，组内按创建顺序。剧详情用。
func (s *Service) ListBySeries(ctx context.Context, seriesID int64) (map[int64][]View, error) {
	bindings, err := s.q.ListBindingsBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of series %d: %w", seriesID, err)
	}
	views := make(map[int64][]View) // 集 ID → 这一集的绑定
	for _, b := range bindings {
		v, err := s.view(b)
		if err != nil {
			return nil, err
		}
		views[b.EpisodeID] = append(views[b.EpisodeID], v)
	}
	return views, nil
}

var (
	// ErrEpisodeGone CreateBackfilledInTx：拉取期间对应的集被删除。
	ErrEpisodeGone = errors.New("episode deleted")
	// ErrSourceBound CreateBackfilledInTx：拉取期间有人在对应的集上绑定了同一个弹幕源。
	ErrSourceBound = errors.New("source already bound")
)

// Backfilled 季绑定补建出的一个绑定：建在哪一集、弹幕源、建出它的季绑定，以及在事务之外拉取的结果。
type Backfilled struct {
	EpisodeID       int64
	Adapter         string
	Ref             []byte
	SeasonBindingID int64
	Fetched         source.Fetched
	CreatedAt       time.Time // 建出时间，也是拉取时间；定时拉取按它算窗口
}

// Saved 写入一次拉取的结果，事务提交之后交给 LogFetched 记日志。
type Saved struct {
	Added   int64 // 新增的弹幕条数
	binding bindingdb.Binding
	fetched source.Fetched
}

// CreateBackfilledInTx 在季绑定补建的写入事务里建出一个绑定：锁住集（拉取期间被删除时返回 ErrEpisodeGone），
// 插入带来源季绑定的绑定（集上已有同一个弹幕源时返回 ErrSourceBound，什么都不写），写入弹幕。
// 调用方开事务并先锁住季和季绑定，提交之后调用 LogFetched。
func (s *Service) CreateBackfilledInTx(ctx context.Context, tx pgx.Tx, b Backfilled) (Saved, error) {
	q := s.q.WithTx(tx)
	if err := lockEpisode(ctx, q, b.EpisodeID, ErrEpisodeGone); err != nil {
		return Saved{}, err
	}
	id, err := q.InsertBackfilledBinding(ctx, bindingdb.InsertBackfilledBindingParams{
		EpisodeID:       b.EpisodeID,
		Adapter:         b.Adapter,
		Ref:             b.Ref,
		Title:           b.Fetched.Title,
		Duration:        int32(b.Fetched.Duration),
		SeasonBindingID: b.SeasonBindingID,
		CreatedAt:       b.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Saved{}, ErrSourceBound
	}
	if err != nil {
		return Saved{}, fmt.Errorf("insert binding of episode %d: %w", b.EpisodeID, err)
	}
	binding, added, err := saveFetched(ctx, q, id, b.Fetched, false, b.CreatedAt)
	if err != nil {
		return Saved{}, err
	}
	return Saved{Added: added, binding: binding, fetched: b.Fetched}, nil
}

// LogFetched 补建出的绑定写入提交之后记一条 "danmaku fetched"，与其他拉取的日志相同。
func (s *Service) LogFetched(ctx context.Context, saved Saved) {
	s.logFetched(ctx, saved.binding, saved.fetched, saved.Added)
}

// Refetch 重新拉取一个绑定的全部弹幕，返回更新后的绑定和新增条数，见 refetch；适配器的错误转成管理 API 的错误。
func (s *Service) Refetch(ctx context.Context, id int64, replace bool) (View, int64, error) {
	view, added, err := s.refetch(ctx, id, replace)
	return view, added, SourceAPIError(err)
}

// refetch Refetch 的实现，适配器的错误原样返回（*source.Error）。不依赖 HTTP 请求，
// 定时拉取（ScheduledFetchService）也复用它，按 Kind 决定是否停下这一轮。
//   - replace 为 false（重新拉取）：只插入新弹幕，从不删除，平台上已经删掉的弹幕继续保留。
//   - replace 为 true（清空后重新拉取，即管理 API 的 clear）：拉取成功后，在同一个事务里删掉这个绑定的全部弹幕、
//     写入这次的结果；新增条数为这次的总条数。
//
// 拉取失败时只记下尝试拉取的时间（定时拉取等满一个间隔再试）；弹幕源不存在（NotFound）时把绑定标为失效，已保存的弹幕保留；
// 失效的绑定拉取成功后恢复正常。
// 拉取期间绑定被删除时返回 404"绑定已被删除"，拉取结果丢弃。用弹幕文件建的绑定不能重新拉取（400）。
func (s *Service) refetch(ctx context.Context, id int64, replace bool) (View, int64, error) {
	b, err := s.getBinding(ctx, id)
	if err != nil {
		return View{}, 0, err
	}
	if b.Kind != kindLink {
		return View{}, 0, errNotLinkBinding
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return View{}, 0, err
	}

	fetched, err := fetch(ctx, adapter, b.Ref)
	if err != nil {
		switch srcErr, ok := errors.AsType[*source.Error](err); {
		case !ok || ctx.Err() != nil: // 服务器内部错误、关闭服务：不算一次尝试
		case srcErr.Kind == source.NotFound:
			if err := s.markDead(ctx, b, srcErr); err != nil {
				return View{}, 0, err
			}
		default:
			if err := s.recordFetchAttempt(ctx, b.ID); err != nil {
				return View{}, 0, err
			}
		}
		return View{}, 0, err
	}
	fetchedAt := time.Now()

	var added int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, id); err != nil {
			return err
		}
		var err error
		b, added, err = saveFetched(ctx, q, id, fetched, replace, fetchedAt)
		return err
	})
	if err != nil {
		return View{}, 0, err
	}
	s.logFetched(ctx, b, fetched, added)
	view, err := s.view(b)
	return view, added, err
}

// markDead 重新拉取时弹幕源已不存在：把绑定标为失效，已保存的弹幕保留。
// 成功后记一条 info 日志，连同适配器给的原因：422 不经过 errorHandler 的日志，定时拉取时也能看出绑定失效了。
func (s *Service) markDead(ctx context.Context, b bindingdb.Binding, reason error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, b.ID); err != nil {
			return err
		}
		if err := q.MarkBindingDead(ctx, bindingdb.MarkBindingDeadParams{ID: b.ID, FetchedAt: time.Now()}); err != nil {
			return fmt.Errorf("mark binding %d dead: %w", b.ID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "binding marked dead",
		slog.Int64("binding_id", b.ID), slog.String("adapter", emptyIfNull(b.Adapter)), slog.String("reason", reason.Error()))
	return nil
}

// recordFetchAttempt 拉取失败（弹幕源不存在之外）时记下尝试拉取的时间，单条语句；绑定已被删除时什么都不做。
func (s *Service) recordFetchAttempt(ctx context.Context, id int64) error {
	if err := s.q.RecordFetchAttempt(ctx, bindingdb.RecordFetchAttemptParams{ID: id, AttemptedAt: time.Now()}); err != nil {
		return fmt.Errorf("record fetch attempt of binding %d: %w", id, err)
	}
	return nil
}

// lockEpisode 写入事务里锁住一集到提交（FOR KEY SHARE），期间删不掉它；这一集已被删除时返回 gone。
func lockEpisode(ctx context.Context, q *bindingdb.Queries, id int64, gone error) error {
	if _, err := q.LockEpisode(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gone
		}
		return fmt.Errorf("lock episode %d: %w", id, err)
	}
	return nil
}

// getBinding 取出绑定，不存在时返回 404"绑定不存在"。
func (s *Service) getBinding(ctx context.Context, id int64) (bindingdb.Binding, error) {
	b, err := s.q.GetBinding(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return bindingdb.Binding{}, errBindingNotFound
		}
		return bindingdb.Binding{}, fmt.Errorf("get binding %d: %w", id, err)
	}
	return b, nil
}

// lockBinding 重新拉取、追加文件、重新解析的写入事务的第一句：锁住这个绑定到提交，同一个绑定的写入排队执行。
// 绑定已被删除时返回 404"绑定已被删除"。
func lockBinding(ctx context.Context, q *bindingdb.Queries, id int64) error {
	if _, err := q.LockBinding(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errBindingDeleted
		}
		return fmt.Errorf("lock binding %d: %w", id, err)
	}
	return nil
}

// UpdateOffset 改偏移（秒，正数表示弹幕延后），单条语句，content_version 不变。取值范围由调用方校验。
func (s *Service) UpdateOffset(ctx context.Context, id int64, offset float64) (View, error) {
	b, err := s.q.UpdateBindingOffset(ctx, bindingdb.UpdateBindingOffsetParams{ID: id, Offset: offset})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, errBindingNotFound
		}
		return View{}, fmt.Errorf("update offset of binding %d: %w", id, err)
	}
	return s.view(b)
}

// Delete 删除绑定，它的弹幕随外键级联删除。单条语句；进行中的拉取写回时会发现绑定已被删除。
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteBinding(ctx, id)
	if err != nil {
		return fmt.Errorf("delete binding %d: %w", id, err)
	}
	if n == 0 {
		return errBindingNotFound
	}
	return nil
}

// saveFetched 在写入事务里保存一次拉取的结果：更新标题、时长与拉取时间 fetchedAt，再用 writeDanmaku 写入弹幕。
// 调用方已在同一个事务里锁住或刚插入这个绑定。返回更新后的绑定和新增条数（replace 时即这次的总条数）。
//
// 拉取时间（也是上次尝试拉取的时间）取自应用的时钟（拉取完成时的 time.Now()），不用数据库的 now()：
// 定时拉取按它判断是否到期，与建出时间用同一个时钟，测试里也能用假时间推进。
func saveFetched(ctx context.Context, q *bindingdb.Queries, bindingID int64, f source.Fetched, replace bool, fetchedAt time.Time) (bindingdb.Binding, int64, error) {
	err := q.RecordFetch(ctx, bindingdb.RecordFetchParams{
		ID:        bindingID,
		Title:     f.Title,
		Duration:  int32(f.Duration),
		FetchedAt: fetchedAt,
	})
	if err != nil {
		return bindingdb.Binding{}, 0, fmt.Errorf("record fetch of binding %d: %w", bindingID, err)
	}
	return writeDanmaku(ctx, q, bindingID, f.Danmaku, replace)
}

// writeDanmaku 在写入事务里写入一批弹幕并更新计数：replace 时先删掉这个绑定的全部弹幕；
// 插入弹幕（按原始 ID 去重，已有的跳过），再用 recordDanmaku 更新计数。返回更新后的绑定和新增条数。
func writeDanmaku(ctx context.Context, q *bindingdb.Queries, bindingID int64, items []danmaku.Danmaku, replace bool) (bindingdb.Binding, int64, error) {
	if replace {
		if err := q.DeleteDanmaku(ctx, bindingID); err != nil {
			return bindingdb.Binding{}, 0, fmt.Errorf("delete danmaku of binding %d: %w", bindingID, err)
		}
	}
	added, err := insertDanmaku(ctx, q, bindingID, items)
	if err != nil {
		return bindingdb.Binding{}, 0, err
	}
	b, err := recordDanmaku(ctx, q, bindingID, replace, added, latestTime(items))
	return b, added, err
}

// recordDanmaku 写入弹幕之后更新绑定的 danmaku_count、content_version 与 max_time_ms（规则见 RecordDanmaku），返回更新后的绑定。
// latestMs 是这次写入的整批弹幕的 latestTime。
func recordDanmaku(ctx context.Context, q *bindingdb.Queries, bindingID int64, replace bool, added int64, latestMs int32) (bindingdb.Binding, error) {
	b, err := q.RecordDanmaku(ctx, bindingdb.RecordDanmakuParams{
		ID: bindingID, Replace: replace, Added: int32(added), LatestMs: latestMs,
	})
	if err != nil {
		return bindingdb.Binding{}, fmt.Errorf("record danmaku of binding %d: %w", bindingID, err)
	}
	return b, nil
}

// latestTime 一批弹幕里最晚的时间，不早于 0（没有弹幕时为 0）。
func latestTime(items []danmaku.Danmaku) int32 {
	var latest int32
	for _, d := range items {
		latest = max(latest, d.TimeMs)
	}
	return latest
}

// insertDanmaku 一条语句写入一批弹幕，按原始 ID 去重（已有的跳过），返回实际插入的条数。
func insertDanmaku(ctx context.Context, q *bindingdb.Queries, bindingID int64, items []danmaku.Danmaku) (int64, error) {
	p := bindingdb.InsertDanmakuParams{
		BindingID: bindingID,
		SourceIds: make([]int64, len(items)),
		TimeMs:    make([]int32, len(items)),
		Modes:     make([]int16, len(items)),
		Colors:    make([]int32, len(items)),
		Texts:     make([]string, len(items)),
	}
	for i, d := range items {
		p.SourceIds[i] = d.SourceID
		p.TimeMs[i] = d.TimeMs
		p.Modes[i] = int16(d.Mode)
		p.Colors[i] = int32(d.Color)
		p.Texts[i] = d.Text
	}
	added, err := q.InsertDanmaku(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("insert danmaku of binding %d: %w", bindingID, err)
	}
	return added, nil
}

// logFetched 每次拉取结束记一条 info 日志：适配器自己的统计加上新增条数和拉取后的总条数；
// 季绑定建出的绑定另记 season_binding_id，能和 "backfill finished" 对上。
func (s *Service) logFetched(ctx context.Context, b bindingdb.Binding, f source.Fetched, added int64) {
	attrs := []slog.Attr{slog.Int64("binding_id", b.ID), slog.Int64("episode_id", b.EpisodeID), slog.String("adapter", emptyIfNull(b.Adapter))}
	if b.SeasonBindingID != nil {
		attrs = append(attrs, slog.Int64("season_binding_id", *b.SeasonBindingID))
	}
	attrs = append(attrs, f.LogAttrs...)
	attrs = append(attrs, slog.Int64("added", added), slog.Int("total", int(b.DanmakuCount)))
	s.logger.LogAttrs(ctx, slog.LevelInfo, "danmaku fetched", attrs...)
}

// view 绑定的 JSON：贴链接建的，弹幕源的链接和标签交给它的适配器生成；用弹幕文件建的，标签写明文件的份数。
func (s *Service) view(b bindingdb.Binding) (View, error) {
	v := View{
		ID:              b.ID,
		Kind:            b.Kind,
		Adapter:         b.Adapter,
		Title:           b.Title,
		Duration:        b.Duration,
		Offset:          b.Offset,
		Status:          b.Status,
		DanmakuCount:    b.DanmakuCount,
		ContentVersion:  b.ContentVersion,
		MaxTimeMs:       b.MaxTimeMs,
		LastFetchedAt:   b.LastFetchedAt,
		SeasonBindingID: b.SeasonBindingID,
	}
	if b.Kind == kindFile {
		v.SourceLabel = fmt.Sprintf("弹幕文件 · %d 份", b.FileCount)
		return v, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return View{}, err
	}
	d, err := adapter.Describe(b.Ref)
	if err != nil {
		return View{}, fmt.Errorf("describe binding %d: %w", b.ID, err)
	}
	v.SourceURL, v.SourceLabel = &d.URL, d.Label
	return v, nil
}

// platform 绑定的弹幕所在的平台：贴链接建的取自适配器；弹幕文件里的弹幕不属于任何平台（见 docs/adr/0004）。
func (s *Service) platform(b bindingdb.Binding) (danmaku.Platform, error) {
	if b.Kind == kindFile {
		return danmaku.PlatformNone, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return "", err
	}
	return adapter.Platform(), nil
}

// linkAdapter 贴链接建的绑定的适配器。用弹幕文件建的绑定没有适配器，调用方要先按 kind 分支；
// 适配器没有注册时返回错误，调用方按服务器内部错误处理。
func (s *Service) linkAdapter(b bindingdb.Binding) (source.Adapter, error) {
	if b.Adapter == nil {
		return nil, fmt.Errorf("binding %d: %s binding has no adapter", b.ID, b.Kind)
	}
	adapter, err := s.sources.Get(*b.Adapter)
	if err != nil {
		return nil, fmt.Errorf("binding %d: %w", b.ID, err)
	}
	return adapter, nil
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

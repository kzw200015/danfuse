package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// fetchTimeout 一次拉取的总时限：server.write_timeout 是 30 秒，留出写库和响应的时间。超时由适配器按 Upstream 返回。
const fetchTimeout = 25 * time.Second

var (
	errEpisodeNotFound = errcode.ErrNotFound.WithMessage("集不存在")
	errEpisodeDeleted  = errcode.ErrNotFound.WithMessage("这一集已被删除")
	errBindingExists   = errcode.ErrConflict.WithMessage("这一集已经绑定过这个来源")
)

// BindingService 绑定：贴链接创建，拉取弹幕源的全部弹幕写入 snapshot。
// 拉取（网络请求）都在事务之外，拉完才开写入事务，写入事务的第一句锁住要写的父行；
// 不加应用层的锁，并发靠行锁、外键级联和唯一约束。
type BindingService struct {
	store   repository.Store
	sources *source.Registry
	logger  *slog.Logger
}

func NewBindingService(store repository.Store, sources *source.Registry, logger *slog.Logger) *BindingService {
	return &BindingService{store: store, sources: sources, logger: logger}
}

// BindingView 绑定的 JSON。不对外暴露原始 ref 和 contentVersion，sourceUrl / sourceLabel 由适配器的 Describe 生成。
type BindingView struct {
	ID            int64      `json:"id"`
	Adapter       string     `json:"adapter"`
	SourceURL     string     `json:"sourceUrl"`
	SourceLabel   string     `json:"sourceLabel"`
	Title         string     `json:"title"`    // 弹幕源的标题
	Duration      int32      `json:"duration"` // 弹幕源视频的时长，秒
	Offset        float64    `json:"offset"`   // 秒，正数表示弹幕延后
	Status        string     `json:"status"`   // active | dead
	DanmakuCount  int32      `json:"danmakuCount"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
}

// Create 贴链接创建绑定：解析链接 → 确认这一集存在（404）→ 查重（409）→ 拉取 → 写入。拉取失败就不创建。
// 拉取期间这一集被删除时返回 404"这一集已被删除"；同时两次给同一集贴同一个弹幕源时，后提交的撞上唯一约束返回 409。
func (s *BindingService) Create(ctx context.Context, episodeID int64, link string) (BindingView, error) {
	adapter, ref, err := s.sources.ParseLink(ctx, link)
	if err != nil {
		return BindingView{}, sourceError(err)
	}
	switch exists, err := s.store.EpisodeExists(ctx, episodeID); {
	case err != nil:
		return BindingView{}, fmt.Errorf("check episode %d: %w", episodeID, err)
	case !exists:
		return BindingView{}, errEpisodeNotFound
	}
	// 只是省掉一次注定 409 的拉取；并发时以写入事务里的唯一约束为准
	key := repository.BindingExistsParams{EpisodeID: episodeID, Adapter: adapter.ID(), Ref: ref}
	switch bound, err := s.store.BindingExists(ctx, key); {
	case err != nil:
		return BindingView{}, fmt.Errorf("check binding of episode %d: %w", episodeID, err)
	case bound:
		return BindingView{}, errBindingExists
	}

	fetched, err := fetch(ctx, adapter, ref)
	if err != nil {
		return BindingView{}, sourceError(err)
	}

	var (
		binding repository.Binding
		added   int64
	)
	err = s.store.ExecTx(ctx, func(q repository.Querier) error {
		// 锁住这一集到提交：之后的删除要等这个事务提交，再连同绑定和弹幕一起删掉
		if _, err := q.LockEpisode(ctx, episodeID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errEpisodeDeleted
			}
			return fmt.Errorf("lock episode %d: %w", episodeID, err)
		}
		id, err := q.InsertBinding(ctx, repository.InsertBindingParams{
			EpisodeID: episodeID,
			Adapter:   adapter.ID(),
			Ref:       ref,
			Title:     fetched.Title,
			Duration:  int32(fetched.Duration),
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errBindingExists
			}
			return fmt.Errorf("insert binding of episode %d: %w", episodeID, err)
		}
		binding, added, err = saveFetched(ctx, q, id, fetched)
		return err
	})
	if err != nil {
		return BindingView{}, err
	}
	s.logFetched(ctx, binding, fetched, added)
	return bindingView(s.sources, binding)
}

// fetch 拉取一个弹幕源的全部弹幕，总时限 fetchTimeout。调用方拉完才开写入事务。
func fetch(ctx context.Context, adapter source.Adapter, ref source.Ref) (source.Fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	return adapter.Fetch(ctx, ref)
}

// saveFetched 在写入事务里保存一次拉取的结果：插入弹幕（按原始 ID 去重，已有的跳过），
// 再更新绑定的计数、标题、时长与拉取时间。调用方已在同一个事务里锁住或刚插入这个绑定。
// 返回更新后的绑定和新增条数。
func saveFetched(ctx context.Context, q repository.Querier, bindingID int64, f source.Fetched) (repository.Binding, int64, error) {
	p := repository.InsertDanmakuParams{
		BindingID: bindingID,
		SourceIds: make([]int64, len(f.Danmaku)),
		TimeMs:    make([]int32, len(f.Danmaku)),
		Modes:     make([]int16, len(f.Danmaku)),
		Colors:    make([]int32, len(f.Danmaku)),
		Texts:     make([]string, len(f.Danmaku)),
	}
	for i, d := range f.Danmaku {
		p.SourceIds[i] = d.SourceID
		p.TimeMs[i] = d.TimeMs
		p.Modes[i] = int16(d.Mode)
		p.Colors[i] = int32(d.Color)
		p.Texts[i] = d.Text
	}
	added, err := q.InsertDanmaku(ctx, p)
	if err != nil {
		return repository.Binding{}, 0, fmt.Errorf("insert danmaku of binding %d: %w", bindingID, err)
	}
	b, err := q.RecordFetch(ctx, repository.RecordFetchParams{
		ID:       bindingID,
		Added:    int32(added),
		Title:    f.Title,
		Duration: int32(f.Duration),
	})
	if err != nil {
		return repository.Binding{}, 0, fmt.Errorf("record fetch of binding %d: %w", bindingID, err)
	}
	return b, added, nil
}

// logFetched 每次拉取结束记一条 info 日志：适配器自己的统计加上新增条数。
func (s *BindingService) logFetched(ctx context.Context, b repository.Binding, f source.Fetched, added int64) {
	attrs := []slog.Attr{slog.Int64("binding_id", b.ID), slog.String("adapter", b.Adapter)}
	attrs = append(attrs, f.LogAttrs...)
	attrs = append(attrs, slog.Int64("added", added))
	s.logger.LogAttrs(ctx, slog.LevelInfo, "danmaku fetched", attrs...)
}

// bindingView 绑定的 JSON，来源链接和标签交给它的适配器生成。
func bindingView(sources *source.Registry, b repository.Binding) (BindingView, error) {
	adapter, ok := sources.Get(b.Adapter)
	if !ok {
		return BindingView{}, fmt.Errorf("binding %d: unknown adapter %q", b.ID, b.Adapter)
	}
	d, err := adapter.Describe(b.Ref)
	if err != nil {
		return BindingView{}, fmt.Errorf("describe binding %d: %w", b.ID, err)
	}
	return BindingView{
		ID:            b.ID,
		Adapter:       b.Adapter,
		SourceURL:     d.URL,
		SourceLabel:   d.Label,
		Title:         b.Title,
		Duration:      b.Duration,
		Offset:        b.Offset,
		Status:        b.Status,
		DanmakuCount:  b.DanmakuCount,
		LastFetchedAt: b.LastFetchedAt,
	}, nil
}

// sourceError 把适配器的 *source.Error 转成管理 API 的错误：InvalidLink 为 400，NotFound 为 422，其余为 502；
// 提示用适配器写的 Message，底层原因只进日志。其他错误原样返回，按服务器内部错误处理。
func sourceError(err error) error {
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		return err
	}
	base := errcode.ErrBadGateway
	switch srcErr.Kind {
	case source.InvalidLink:
		base = errcode.ErrBadRequest
	case source.NotFound:
		base = errcode.ErrUnprocessable
	}
	return base.WithMessage(srcErr.Message).Wrap(err)
}

// Package blockword 屏蔽词：全局规则，弹弹 API 输出一集的弹幕时去掉正文命中的，保存的弹幕不受影响。
// 匹配规则在 danmaku 包（danmaku.BlockedWord、danmaku.Blocklist），这里只管保存与增删。
package blockword

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/blockword/blockworddb"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

var (
	errDuplicated = apierr.ErrConflict.WithMessage("已有相同的屏蔽词")
	errNotFound   = apierr.ErrNotFound.WithMessage("屏蔽词不存在")
)

// Service 屏蔽词的增删与读取。
type Service struct {
	q *blockworddb.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: blockworddb.New(pool)}
}

// BlockedWord 管理界面看到的一条屏蔽词。
type BlockedWord struct {
	ID        int64                   `json:"id"`
	Kind      danmaku.BlockedWordKind `json:"kind"` // keyword 关键词、regex 正则
	Pattern   string                  `json:"pattern"`
	CreatedAt time.Time               `json:"createdAt"`
}

// List 全部屏蔽词，新加的在前。
func (s *Service) List(ctx context.Context) ([]BlockedWord, error) {
	rows, err := s.q.ListBlockedWords(ctx)
	if err != nil {
		return nil, fmt.Errorf("list blocked words: %w", err)
	}
	words := make([]BlockedWord, len(rows))
	for i, r := range rows {
		words[i] = BlockedWord{ID: r.ID, Kind: danmaku.BlockedWordKind(r.Kind), Pattern: r.Pattern, CreatedAt: r.CreatedAt}
	}
	return words, nil
}

// Create 新增一条屏蔽词，w 已经过 danmaku.ParseBlockedWord 校验。同类型、Key 相同的已经有了时 409。
func (s *Service) Create(ctx context.Context, w danmaku.BlockedWord) (BlockedWord, error) {
	r, err := s.q.CreateBlockedWord(ctx, blockworddb.CreateBlockedWordParams{Kind: string(w.Kind), Pattern: w.Pattern, DedupKey: w.Key()})
	if database.IsUniqueViolation(err) {
		return BlockedWord{}, errDuplicated
	}
	if err != nil {
		return BlockedWord{}, fmt.Errorf("create blocked word: %w", err)
	}
	return BlockedWord{ID: r.ID, Kind: danmaku.BlockedWordKind(r.Kind), Pattern: r.Pattern, CreatedAt: r.CreatedAt}, nil
}

// Delete 删除一条屏蔽词，不存在时 404。
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteBlockedWord(ctx, id)
	if err != nil {
		return fmt.Errorf("delete blocked word %d: %w", id, err)
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

// Blocklist 编译全部屏蔽词，供弹弹 API 取一集的弹幕时使用。每次现读现编译、不缓存：屏蔽词改了下次取弹幕就生效。
func (s *Service) Blocklist(ctx context.Context) (danmaku.Blocklist, error) {
	rows, err := s.q.ListBlockedWords(ctx)
	if err != nil {
		return danmaku.Blocklist{}, fmt.Errorf("list blocked words: %w", err)
	}
	words := make([]danmaku.BlockedWord, len(rows))
	for i, r := range rows {
		words[i] = danmaku.BlockedWord{Kind: danmaku.BlockedWordKind(r.Kind), Pattern: r.Pattern}
	}
	return danmaku.NewBlocklist(words)
}

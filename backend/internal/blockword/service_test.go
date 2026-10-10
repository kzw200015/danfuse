package blockword_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/blockword"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// describe 把屏蔽词写成 "类型 内容"，便于整体比较。
func describe(words []blockword.BlockedWord) []string {
	got := make([]string, len(words))
	for i, w := range words {
		got[i] = string(w.Kind) + " " + w.Pattern
	}
	return got
}

func TestCreateListDelete(t *testing.T) {
	t.Parallel()
	svc := blockword.NewService(dbtest.Pool(t))
	ctx := t.Context()

	var ids []int64
	for _, w := range []danmaku.BlockedWord{
		{Kind: danmaku.BlockedKeyword, Pattern: "AWSL"},
		{Kind: danmaku.BlockedRegex, Pattern: "AWSL"}, // 内容相同、类型不同，不算重复
		{Kind: danmaku.BlockedRegex, Pattern: "awsl"}, // 正则按原文判断重复
	} {
		created, err := svc.Create(ctx, w)
		if err != nil {
			t.Fatalf("Create(%+v): %v", w, err)
		}
		if created.Kind != w.Kind || created.Pattern != w.Pattern || created.CreatedAt.IsZero() {
			t.Errorf("Create(%+v) = %+v", w, created)
		}
		ids = append(ids, created.ID)
	}

	// 关键词按归一化后的内容判断重复
	_, err := svc.Create(ctx, danmaku.BlockedWord{Kind: danmaku.BlockedKeyword, Pattern: "ａ w s l"})
	testenv.AssertAppError(t, err, http.StatusConflict, "已有相同的屏蔽词")
	_, err = svc.Create(ctx, danmaku.BlockedWord{Kind: danmaku.BlockedRegex, Pattern: "AWSL"})
	testenv.AssertAppError(t, err, http.StatusConflict, "已有相同的屏蔽词")

	words, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(words), []string{"regex awsl", "regex AWSL", "keyword AWSL"}; !slices.Equal(got, want) {
		t.Errorf("List() = %q, want 新加的在前 %q", got, want)
	}

	if err := svc.Delete(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	testenv.AssertAppError(t, svc.Delete(ctx, ids[1]), http.StatusNotFound, "屏蔽词不存在")
	words, err = svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(words), []string{"regex awsl", "keyword AWSL"}; !slices.Equal(got, want) {
		t.Errorf("删除后 List() = %q, want %q", got, want)
	}
}

// TestCommentsBlocked 弹弹 API 取一集的弹幕时去掉命中屏蔽词的；屏蔽词改了（包括在别的实例上改的）下次取弹幕就生效，保存的弹幕不受影响。
func TestCommentsBlocked(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	testenv.SeedEpisodes(t, pool)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO bindings (episode_id, kind, title, danmaku_count, max_time_ms) VALUES (1, 'file', '弹幕文件', 3, 3000);
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES
			(1, 1, 1000, 1, 0, '前排'),
			(1, 2, 2000, 1, 0, '前方剧透'),
			(1, 3, 3000, 1, 0, '2333');`)
	if err != nil {
		t.Fatal(err)
	}
	env := testenv.New(pool, testenv.Logger(t))
	comments := func() []string {
		t.Helper()
		items, err := env.Dandan.Comments(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		texts := make([]string, len(items))
		for i, it := range items {
			texts[i] = it.Text
		}
		return texts
	}

	spoiler, err := env.BlockedWords.Create(t.Context(), danmaku.BlockedWord{Kind: danmaku.BlockedKeyword, Pattern: "剧透"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.BlockedWords.Create(t.Context(), danmaku.BlockedWord{Kind: danmaku.BlockedRegex, Pattern: `^\d+$`}); err != nil {
		t.Fatal(err)
	}
	if got, want := comments(), []string{"前排"}; !slices.Equal(got, want) {
		t.Errorf("Comments() = %q, want %q", got, want)
	}

	if err := env.BlockedWords.Delete(t.Context(), spoiler.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := comments(), []string{"前排", "前方剧透"}; !slices.Equal(got, want) {
		t.Errorf("删掉屏蔽词后 Comments() = %q, want %q", got, want)
	}

	// 别的实例加的屏蔽词（这里直接写表）下次取弹幕也生效：编译好的屏蔽词只在表里的内容没变时复用
	if _, err := pool.Exec(t.Context(), `INSERT INTO blocked_words (kind, pattern, dedup_key) VALUES ('keyword', '前排', '前排')`); err != nil {
		t.Fatal(err)
	}
	if got, want := comments(), []string{"前方剧透"}; !slices.Equal(got, want) {
		t.Errorf("别的实例加了屏蔽词后 Comments() = %q, want %q", got, want)
	}
	testenv.AssertInvariants(t, pool)
}

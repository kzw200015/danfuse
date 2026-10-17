package service_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/export/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
)

// exportSeason 导出季 id 并读回 zip。
func exportSeason(t *testing.T, svc *service.Service, id int64) exportedZip {
	t.Helper()
	return exportZip(t, func(open service.Open) error { return svc.ExportSeason(t.Context(), id, open) })
}

// TestExportSeason 一季导出为 <剧名> S01.zip，里面一层根目录 <剧名> S01/，每集一个 S01E03/ 子目录，
// 放这一集各绑定的 <绑定标题>.xml（同 ExportEpisode）。集按集号排序（三位集号照常写），集内的绑定按创建顺序；
// 没有可导出绑定的集不建子目录，别的季的绑定不在里面。
func TestExportSeason(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO seasons (series_id, number) VALUES (1, 2); -- 季 2
		INSERT INTO episodes (season_id, number) VALUES
			(1, 100), -- 集 3
			(1, 3),   -- 集 4：绑定没有弹幕
			(2, 1);   -- 集 5：别的季`)
	if err != nil {
		t.Fatal(err)
	}
	d := func(id int64, ms int32, text string) danmaku.Danmaku {
		return danmaku.Danmaku{SourceID: id, TimeMs: ms, Mode: danmaku.ModeScroll, Text: text}
	}
	insertEpisodeBinding(t, pool, 3, "OP", 0, d(1, 100, "第一百集"))
	insertBinding(t, pool, "弹幕源 b", 0, d(2, 200, "b"))
	insertBinding(t, pool, "弹幕源 a", 1, d(3, 300, "a"))
	insertEpisodeBinding(t, pool, 4, "空的", 0)
	insertEpisodeBinding(t, pool, 5, "别的季", 0, d(4, 400, "不在这季"))

	got := exportSeason(t, svc, 1)

	if got.Name != "星海旅人 S01.zip" {
		t.Errorf("zip 文件名 = %q, want %q", got.Name, "星海旅人 S01.zip")
	}
	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{
		"星海旅人 S01/S01E01/弹幕源 b.xml",
		"星海旅人 S01/S01E01/弹幕源 a.xml",
		"星海旅人 S01/S01E100/OP.xml",
	})
	testutil.AssertStrings(t, "弹幕源 a.xml", got.Danmaku["星海旅人 S01/S01E01/弹幕源 a.xml"], []string{"1300 1 000000 3 a"})
	testutil.AssertStrings(t, "OP.xml", got.Danmaku["星海旅人 S01/S01E100/OP.xml"], []string{"100 1 000000 1 第一百集"})
}

// TestExportSeasonSpecials 特别篇（第 0 季）导出为 <剧名> S00.zip，子目录为 S00E01；剧名里的非法字符换成 _。
func TestExportSeasonSpecials(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	_, err := pool.Exec(t.Context(), `
		UPDATE series SET title = 'A/B: 前传' WHERE id = 1;
		INSERT INTO seasons (series_id, number) VALUES (1, 0); -- 季 2：特别篇
		INSERT INTO episodes (season_id, number) VALUES (2, 1); -- 集 3`)
	if err != nil {
		t.Fatal(err)
	}
	insertEpisodeBinding(t, pool, 3, "OVA", 0, danmaku.Danmaku{SourceID: 1, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "有"})

	got := exportSeason(t, svc, 2)

	if got.Name != "A_B_ 前传 S00.zip" {
		t.Errorf("zip 文件名 = %q, want %q", got.Name, "A_B_ 前传 S00.zip")
	}
	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{"A_B_ 前传 S00/S00E01/OVA.xml"})
}

// gatedWriter 第一次 Write 时在 started 上报到，等 release 关闭再写进 Buffer：用来在导出途中插入别的操作。
type gatedWriter struct {
	bytes.Buffer
	ctx              context.Context
	started, release chan struct{}
	waited           bool
}

func newGatedWriter(ctx context.Context) *gatedWriter {
	return &gatedWriter{ctx: ctx, started: make(chan struct{}), release: make(chan struct{})}
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	if !w.waited {
		w.waited = true
		w.started <- struct{}{}
		select {
		case <-w.release:
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		}
	}
	return w.Buffer.Write(p)
}

// TestExportSeasonSnapshot 一季的 zip 是同一时刻的快照：导出写到第一集时另一个事务删掉第二集的绑定，zip 里仍是删除前的内容。
// 第一集的弹幕多到超过 zip 的写缓冲，写这一集时就开始写出，导出停在第一次写出上，这时第二集还没读。
func TestExportSeasonSnapshot(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	insertLargeBinding(t, pool, 1, "第一集", 3000)
	second := insertEpisodeBinding(t, pool, 2, "第二集", 0, danmaku.Danmaku{SourceID: 1, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "删除前"})

	w := newGatedWriter(t.Context())
	var name string
	errc := make(chan error, 1)
	go func() {
		errc <- svc.ExportSeason(t.Context(), 1, func(n string) io.Writer {
			name = n
			return w
		})
	}()
	select {
	case <-w.started:
	case err := <-errc:
		t.Fatalf("导出没有在写出时停下，已经返回：%v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM bindings WHERE id = $1`, second); err != nil {
		t.Fatal(err)
	}
	close(w.release)
	if err := <-errc; err != nil {
		t.Fatalf("ExportSeason: %v", err)
	}

	got := readZip(t, name, w.Bytes())
	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{"星海旅人 S01/S01E01/第一集.xml", "星海旅人 S01/S01E02/第二集.xml"})
	testutil.AssertStrings(t, "第二集.xml", got.Danmaku["星海旅人 S01/S01E02/第二集.xml"], []string{"100 1 000000 1 删除前"})
	if n := len(got.Danmaku["星海旅人 S01/S01E01/第一集.xml"]); n != 3000 {
		t.Errorf("第一集.xml 有 %d 条弹幕, want 3000", n)
	}
}

// TestExportSeasonNothingToExport整季没有可导出的弹幕时 404，不调用 open（不输出空的 zip）：
// 没有集，集都没有绑定，或者绑定都没有弹幕、校正后全部丢弃。季不存在时也是 404。
func TestExportSeasonNothingToExport(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO seasons (series_id, number) VALUES (1, 2)`); err != nil { // 季 2：没有集
		t.Fatal(err)
	}
	insertBinding(t, pool, "空的", 0)
	insertEpisodeBinding(t, pool, 2, "太早", -5, danmaku.Danmaku{SourceID: 1, TimeMs: 4999, Mode: danmaku.ModeScroll, Text: "丢弃"})

	tests := []struct {
		id      int64
		message string
	}{
		{1, "这一季没有可导出的弹幕"},
		{2, "这一季没有可导出的弹幕"},
		{99, "季不存在"},
	}
	for _, tt := range tests {
		err := svc.ExportSeason(t.Context(), tt.id, func(name string) io.Writer {
			t.Errorf("季 %d：给出了文件名 %q", tt.id, name)
			return io.Discard
		})
		testutil.AssertAppError(t, err, http.StatusNotFound, tt.message)
	}
}

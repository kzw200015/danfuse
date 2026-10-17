package service_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/export/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
)

// exportEpisode 导出集 id 并读回 zip。
func exportEpisode(t *testing.T, svc *service.Service, id int64) exportedZip {
	t.Helper()
	return exportZip(t, func(open service.Open) error { return svc.ExportEpisode(t.Context(), id, open) })
}

// TestExportEpisode 一集导出为 <剧名> S01E01.zip，里面平铺各绑定的 <绑定标题>.xml（按创建顺序），
// 每份是这个绑定按偏移校正后的弹幕（同 ExportBinding）。
func TestExportEpisode(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	insertBinding(t, pool, "弹幕源 a", 1,
		danmaku.Danmaku{SourceID: 2, TimeMs: 3000, Mode: danmaku.ModeTop, Color: 0xFF0000, Text: "顶部"},
		danmaku.Danmaku{SourceID: 1, TimeMs: 0, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排"},
	)
	insertBinding(t, pool, "20130709", -0.5, danmaku.Danmaku{SourceID: 7, TimeMs: 1500, Mode: danmaku.ModeBottom, Text: "<&>"})
	insertEpisodeBinding(t, pool, 2, "别的集", 0, danmaku.Danmaku{SourceID: 9, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "不在这集"})

	got := exportEpisode(t, svc, 1)

	if got.Name != "星海旅人 S01E01.zip" {
		t.Errorf("zip 文件名 = %q, want %q", got.Name, "星海旅人 S01E01.zip")
	}
	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{"弹幕源 a.xml", "20130709.xml"})
	testutil.AssertStrings(t, "弹幕源 a.xml", got.Danmaku["弹幕源 a.xml"], []string{"1000 1 ffffff 1 前排", "4000 5 ff0000 2 顶部"})
	testutil.AssertStrings(t, "20130709.xml", got.Danmaku["20130709.xml"], []string{"1000 4 000000 7 <&>"})
}

// TestExportEpisodeZipName zip 的文件名：季号、集号补到两位（特别篇为 S00），超过两位照常写；
// 电影只写片名；剧名里的非法字符换成 _。
func TestExportEpisodeZipName(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO series (type, title) VALUES ('movie', '长夜灯塔'), ('tv', 'A/B: 前传');
		INSERT INTO seasons (series_id, number) VALUES (1, 0), (2, 1), (3, 12);
		INSERT INTO episodes (season_id, number) VALUES
			(2, 100), -- 集 3：星海旅人的特别篇
			(3, 1),   -- 集 4：电影
			(4, 7);   -- 集 5`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		episodeID int64
		want      string
	}{
		{1, "星海旅人 S01E01.zip"},
		{3, "星海旅人 S00E100.zip"},
		{4, "长夜灯塔.zip"},
		{5, "A_B_ 前传 S12E07.zip"},
	}
	for _, tt := range tests {
		insertEpisodeBinding(t, pool, tt.episodeID, "弹幕源", 0, danmaku.Danmaku{SourceID: 1, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "有"})
		if got := exportEpisode(t, svc, tt.episodeID); got.Name != tt.want {
			t.Errorf("集 %d：zip 文件名 = %q, want %q", tt.episodeID, got.Name, tt.want)
		}
	}
}

// TestExportEpisodeDuplicateTitles 同名的文件按绑定的创建顺序依次加 " (2)"、" (3)"；加了后缀又与别的绑定的标题重名时继续加。
// 标题为空的用"弹幕"，与标题就是"弹幕"的也算重名。
func TestExportEpisodeDuplicateTitles(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	d := danmaku.Danmaku{SourceID: 1, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "有"}
	for _, title := range []string{"OP", "OP", "", "OP (2)", "弹幕", "OP"} {
		insertBinding(t, pool, title, 0, d)
	}

	got := exportEpisode(t, svc, 1)

	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{
		"OP.xml", "OP (2).xml", "弹幕.xml", "OP (2) (2).xml", "弹幕 (2).xml", "OP (3).xml",
	})
}

// TestExportEpisodeSkipsEmpty 跳过没有弹幕的绑定，校正后全部为负被丢弃的也算没有；跳过的不占文件名。
// 失效的绑定照常导出。
func TestExportEpisodeSkipsEmpty(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	insertBinding(t, pool, "OP", 0)
	insertBinding(t, pool, "太早", -5, danmaku.Danmaku{SourceID: 1, TimeMs: 4999, Mode: danmaku.ModeScroll, Text: "丢弃"})
	dead := insertBinding(t, pool, "OP", 0, danmaku.Danmaku{SourceID: 2, TimeMs: 100, Mode: danmaku.ModeScroll, Text: "还在"})
	if _, err := pool.Exec(t.Context(), `UPDATE bindings SET status = 'dead' WHERE id = $1`, dead); err != nil {
		t.Fatal(err)
	}

	got := exportEpisode(t, svc, 1)

	testutil.AssertStrings(t, "zip 里的文件", got.Paths, []string{"OP.xml"})
	testutil.AssertStrings(t, "OP.xml", got.Danmaku["OP.xml"], []string{"100 1 000000 2 还在"})
}

// TestExportEpisodeNothingToExport 这一集没有可导出的弹幕时 404，不调用 open（不输出空的 zip）：
// 没有绑定，或者绑定都没有弹幕、校正后全部丢弃。集不存在时也是 404。
func TestExportEpisodeNothingToExport(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	insertBinding(t, pool, "空的", 0)
	insertBinding(t, pool, "太早", -5, danmaku.Danmaku{SourceID: 1, TimeMs: 4999, Mode: danmaku.ModeScroll, Text: "丢弃"})

	tests := []struct {
		id      int64
		message string
	}{
		{1, "这一集没有可导出的弹幕"},
		{2, "这一集没有可导出的弹幕"},
		{99, "集不存在"},
	}
	for _, tt := range tests {
		err := svc.ExportEpisode(t.Context(), tt.id, func(name string) io.Writer {
			t.Errorf("集 %d：给出了文件名 %q", tt.id, name)
			return io.Discard
		})
		testutil.AssertAppError(t, err, http.StatusNotFound, tt.message)
	}
}

package service_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/export/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// exportBinding 导出绑定 id，返回给出的文件名和读回的弹幕（decodeXML）。
// 检查文件名恰好给出一次、内容写在给出文件名之后。
func exportBinding(t *testing.T, svc *service.Service, id int64) (string, []string) {
	t.Helper()
	var names []string
	var buf bytes.Buffer
	err := svc.ExportBinding(t.Context(), id, func(name string) io.Writer {
		if buf.Len() > 0 {
			t.Error("给出文件名之前已经写了内容")
		}
		names = append(names, name)
		return &buf
	})
	if err != nil {
		t.Fatalf("ExportBinding(%d): %v", id, err)
	}
	if len(names) != 1 {
		t.Fatalf("文件名给出了 %d 次：%q", len(names), names)
	}
	return names[0], decodeXML(t, buf.Bytes())
}

// TestExportBinding 一个绑定保存的全部弹幕，按偏移校正（t + offset，四舍五入到毫秒）后按校正后的时间升序、同一时间按原始 ID，
// 文件名为绑定标题。
func TestExportBinding(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	id := insertBinding(t, pool, 1, "第 1 话", 1.2346,
		danmaku.Danmaku{SourceID: 9, TimeMs: 3000, Mode: danmaku.ModeTop, Color: 0xFF0000, Text: "顶部"},
		danmaku.Danmaku{SourceID: 5, TimeMs: 0, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排"},
		danmaku.Danmaku{SourceID: 3, TimeMs: 0, Mode: danmaku.ModeBottom, Color: 0, Text: "<&>"},
	)

	name, got := exportBinding(t, svc, id)

	if name != "第 1 话.xml" {
		t.Errorf("文件名 = %q, want %q", name, "第 1 话.xml")
	}
	testutil.AssertStrings(t, "弹幕", got, []string{
		"1235 4 000000 3 <&>",
		"1235 1 ffffff 5 前排",
		"4235 5 ff0000 9 顶部",
	})
}

// TestExportBindingNegativeOffset 负偏移：校正后为负的丢弃，为 0 的保留。
func TestExportBindingNegativeOffset(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	id := insertBinding(t, pool, 1, "提前", -2,
		danmaku.Danmaku{SourceID: 1, TimeMs: 1999, Mode: danmaku.ModeScroll, Text: "太早"},
		danmaku.Danmaku{SourceID: 2, TimeMs: 2000, Mode: danmaku.ModeScroll, Text: "开头"},
		danmaku.Danmaku{SourceID: 3, TimeMs: 62500, Mode: danmaku.ModeScroll, Text: "一分钟"},
	)

	_, got := exportBinding(t, svc, id)

	testutil.AssertStrings(t, "弹幕", got, []string{"0 1 000000 2 开头", "60500 1 000000 3 一分钟"})
}

// TestExportBindingDead 失效的绑定照常导出它保存的弹幕。
func TestExportBindingDead(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	id := insertBinding(t, pool, 1, "已下架", 0, danmaku.Danmaku{SourceID: 1, TimeMs: 500, Mode: danmaku.ModeScroll, Text: "还在"})
	if _, err := pool.Exec(t.Context(), `UPDATE bindings SET status = 'dead' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	_, got := exportBinding(t, svc, id)

	testutil.AssertStrings(t, "弹幕", got, []string{"500 1 000000 1 还在"})
}

// TestExportBindingFromSeasonBinding 季绑定建出的绑定和手动建的一样导出：文件夹的季绑定按子目录建出的文件绑定，标题为条目名称。
func TestExportBindingFromSeasonBinding(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	createFolderSeasonBinding(t, pool, testenv.Snapshot1("星海旅人/01.xml"))
	id := testenv.QueryInt(t, pool, `SELECT id FROM bindings WHERE season_binding_id IS NOT NULL`)

	name, got := exportBinding(t, svc, id)

	if name != "01.xml" {
		t.Errorf("文件名 = %q, want 01.xml", name)
	}
	testutil.AssertStrings(t, "弹幕", got, []string{"1500 1 ffffff 1 前排", "61250 1 ffffff 2 好看"})
}

// TestExportBindingNotFound 绑定不存在时 404，不给出文件名。
func TestExportBindingNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)

	err := svc.ExportBinding(t.Context(), 99, func(name string) io.Writer {
		t.Errorf("给出了文件名 %q", name)
		return io.Discard
	})

	testutil.AssertAppError(t, err, http.StatusNotFound, "绑定不存在")
}

// TestExportBindingFileName 文件名：标题为空用"弹幕"；/ \ : * ? " < > | 与控制字符换成 _；
// 超过 200 字节的截断到 200 字节以内，不切开 UTF-8 字符。
func TestExportBindingFileName(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"空标题", "", "弹幕.xml"},
		{"只有空白", "  ", "弹幕.xml"},
		{"非法字符与控制字符", "a/b\\c:d*e?f\"g<h>i|j\x01k\tl\u0085m", "a_b_c_d_e_f_g_h_i_j_k_l_m.xml"},
		{"正好 200 字节", strings.Repeat("ab", 100), strings.Repeat("ab", 100) + ".xml"},
		{"截断不切开字符", "a" + strings.Repeat("弹", 100), "a" + strings.Repeat("弹", 66) + ".xml"},
	}
	for i, tt := range tests {
		id := insertBinding(t, pool, 1, fmt.Sprint(i), 0)
		if _, err := pool.Exec(t.Context(), `UPDATE bindings SET title = $2 WHERE id = $1`, id, tt.title); err != nil {
			t.Fatal(err)
		}
		if name, _ := exportBinding(t, svc, id); name != tt.want {
			t.Errorf("%s：文件名 = %q, want %q", tt.name, name, tt.want)
		}
	}
}

// TestExportBindingEmpty 没有弹幕的绑定导出一份空的 XML。
func TestExportBindingEmpty(t *testing.T) {
	t.Parallel()
	svc, pool := newService(t)
	id := insertBinding(t, pool, 1, "空的", 0)

	name, got := exportBinding(t, svc, id)

	if name != "空的.xml" || len(got) != 0 {
		t.Errorf("导出 = %q %q, want 空的.xml 里没有弹幕", name, got)
	}
}

package service

import (
	"bytes"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// xmlFile 一份 B 站导出的 XML 弹幕文件。ds 是 <d> 的 p 与正文交替排列。
func xmlFile(name string, ds ...string) UploadedFile {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>934042</chatid>`)
	for i := 0; i < len(ds); i += 2 {
		b.WriteString(`<d p="` + ds[i] + `">` + ds[i+1] + "</d>\n")
	}
	b.WriteString("</i>")
	return UploadedFile{Name: name, Data: []byte(b.String())}
}

// 同一个视频不同日期的两份快照：dmid 2 两份都有（新快照里时间精度不同），dmid 1、3 各在一份里。
var (
	snapshot1 = xmlFile("20130709.xml",
		"1.5,1,25,16777215,1373250214,0,d9df08a7,1", "前排",
		"61.25,1,25,16777215,1373250228,0,500cea0d,2", "好看")
	snapshot2 = xmlFile("20130711.xml",
		"61.2499980927,1,25,16777215,1373250228,0,500cea0d,2", "好看",
		"120,5,25,16711680,1373250271,0,3ae9ab45,3", "顶部")
	snapshotDanmaku = []danmaku.Danmaku{
		{SourceID: 1, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排"},
		{SourceID: 2, TimeMs: 61250, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "好看"},
		{SourceID: 3, TimeMs: 120000, Mode: danmaku.ModeTop, Color: 0xFF0000, Text: "顶部"},
	}
	notDanmaku = UploadedFile{Name: "README.html", Data: []byte("<html><body>历史弹幕</body></html>")}
)

// fileNames 绑定里的弹幕文件名，按加入的顺序。
func fileNames(t *testing.T, svc *BindingService, id int64) []string {
	t.Helper()
	files, err := svc.ListFiles(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return names
}

func TestCreateFromFiles(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	svc, pool := newBindingService(t, &fakeAdapter{}, slogTo(&logs))

	got, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1, snapshot2})
	if err != nil {
		t.Fatal(err)
	}

	want := BindingView{
		ID:           1,
		Kind:         "file",
		SourceLabel:  "弹幕文件 · 2 份",
		Title:        "20130709",
		Status:       "active",
		DanmakuCount: 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CreateFromFiles() = %+v\nwant %+v", got, want)
	}
	if items := readDanmaku(t, pool, got.ID); !slices.Equal(items, snapshotDanmaku) {
		t.Errorf("落库的弹幕 = %+v\nwant %+v", items, snapshotDanmaku)
	}
	if names := fileNames(t, svc, got.ID); !slices.Equal(names, []string{"20130709.xml", "20130711.xml"}) {
		t.Errorf("弹幕文件 = %v", names)
	}
	if v := getBinding(t, pool, got.ID).ContentVersion; v != 1 {
		t.Errorf("content_version = %d, want 1", v)
	}
	if wantLog := `level=INFO msg="danmaku files added" binding_id=1 files=2 added=3`; !strings.Contains(logs.String(), wantLog) {
		t.Errorf("日志 = %q, want 含 %q", logs.String(), wantLog)
	}

	// 同一集可以再建一个用弹幕文件建的绑定，不查重
	if _, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1}); err != nil {
		t.Errorf("同一集再建一个: %v", err)
	}
}

// TestCreateFromFilesSameContent 一次上传里内容相同的两份文件只存一份。
func TestCreateFromFilesSameContent(t *testing.T) {
	t.Parallel()
	svc, _ := newBindingService(t, &fakeAdapter{}, testLogger(t))
	copied := UploadedFile{Name: "副本.xml", Data: snapshot1.Data}

	got, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1, copied})
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceLabel != "弹幕文件 · 1 份" || got.DanmakuCount != 2 {
		t.Errorf("CreateFromFiles() = %+v, want 1 份、2 条", got)
	}
}

func TestCreateFromFilesErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		episodeID int64
		files     []UploadedFile
		status    int
		message   string
	}{
		{
			"有一份认不出：整次不创建", 1,
			[]UploadedFile{snapshot1, notDanmaku},
			http.StatusUnprocessableEntity,
			"无法识别「README.html」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
		},
		{
			"文件被截断", 1,
			[]UploadedFile{{Name: "1.xml", Data: snapshot1.Data[:60]}},
			http.StatusUnprocessableEntity,
			"无法识别「1.xml」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
		},
		{"集不存在", 99, []UploadedFile{snapshot1}, http.StatusNotFound, "集不存在"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, pool := newBindingService(t, &fakeAdapter{}, testLogger(t))

			_, err := svc.CreateFromFiles(t.Context(), tt.episodeID, tt.files)

			assertAppError(t, err, tt.status, tt.message)
			assertNothingWritten(t, pool)
			if n := queryInt(t, pool, `SELECT count(*) FROM binding_files`); n != 0 {
				t.Errorf("存下了 %d 份文件", n)
			}
		})
	}
}

func TestAppendFiles(t *testing.T) {
	t.Parallel()
	svc, pool := newBindingService(t, &fakeAdapter{}, testLogger(t))
	created, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateOffset(t.Context(), created.ID, 2.5); err != nil {
		t.Fatal(err)
	}

	// 一份新的、一份已有的：已有的跳过；新的那份与已有的重叠的弹幕按原始 ID 去重
	got, err := svc.AppendFiles(t.Context(), created.ID, []UploadedFile{snapshot2, snapshot1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 1 || got.Skipped != 1 || got.Added != 1 {
		t.Errorf("AppendFiles() = files %d skipped %d added %d, want 1、1、1", got.Files, got.Skipped, got.Added)
	}
	b := got.Binding
	if b.SourceLabel != "弹幕文件 · 2 份" || b.DanmakuCount != 3 || b.Title != "20130709" || b.Offset != 2.5 {
		t.Errorf("绑定 = %+v, want 2 份、3 条，标题和偏移不变", b)
	}
	if items := readDanmaku(t, pool, created.ID); !slices.Equal(items, snapshotDanmaku) {
		t.Errorf("落库的弹幕 = %+v\nwant %+v", items, snapshotDanmaku)
	}
	if v := getBinding(t, pool, created.ID).ContentVersion; v != 2 {
		t.Errorf("content_version = %d, want 2", v)
	}

	// 全部已有：什么都不改
	got, err = svc.AppendFiles(t.Context(), created.ID, []UploadedFile{snapshot1, snapshot2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 0 || got.Skipped != 2 || got.Added != 0 || got.Binding.SourceLabel != "弹幕文件 · 2 份" {
		t.Errorf("全部已有：AppendFiles() = %+v", got)
	}
	if v := getBinding(t, pool, created.ID).ContentVersion; v != 2 {
		t.Errorf("全部已有：content_version = %d, want 2", v)
	}

	// 有一份认不出：什么都不加
	_, err = svc.AppendFiles(t.Context(), created.ID, []UploadedFile{xmlFile("新.xml", "1,1,25,0,0,0,x,9", "新"), notDanmaku})
	assertAppError(t, err, http.StatusUnprocessableEntity, "无法识别「README.html」：目前只支持 B 站的 XML 弹幕文件，且文件要完整")
	if names := fileNames(t, svc, created.ID); len(names) != 2 {
		t.Errorf("有一份认不出：弹幕文件 = %v, want 仍是 2 份", names)
	}
}

// TestFileBindingKind 只对一种绑定有效的操作，用在另一种上时返回 400；绑定不存在时 404。
func TestFileBindingKind(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, _ := newBindingService(t, adapter, testLogger(t))
	link, err := svc.Create(t.Context(), 1, "fake/s1")
	if err != nil {
		t.Fatal(err)
	}
	file, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1})
	if err != nil {
		t.Fatal(err)
	}

	const notFile = "这个绑定不是用弹幕文件建的"
	_, err = svc.AppendFiles(t.Context(), link.ID, []UploadedFile{snapshot2})
	assertAppError(t, err, http.StatusBadRequest, notFile)
	_, err = svc.Reparse(t.Context(), link.ID)
	assertAppError(t, err, http.StatusBadRequest, notFile)
	_, err = svc.ListFiles(t.Context(), link.ID)
	assertAppError(t, err, http.StatusBadRequest, notFile)
	_, _, err = svc.Refetch(t.Context(), file.ID, false)
	assertAppError(t, err, http.StatusBadRequest, "用弹幕文件建的绑定不能重新拉取")

	_, err = svc.AppendFiles(t.Context(), 99, []UploadedFile{snapshot2})
	assertAppError(t, err, http.StatusNotFound, "绑定不存在")
	_, err = svc.Reparse(t.Context(), 99)
	assertAppError(t, err, http.StatusNotFound, "绑定不存在")
	_, err = svc.ListFiles(t.Context(), 99)
	assertAppError(t, err, http.StatusNotFound, "绑定不存在")

	// 偏移、删除两种绑定都能用
	if _, err := svc.UpdateOffset(t.Context(), file.ID, -1); err != nil {
		t.Errorf("UpdateOffset: %v", err)
	}
	if err := svc.Delete(t.Context(), file.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

// TestReparse 重新解析按保存的文件重建弹幕：解析规则改了以后（这里用直接改库模拟），弹幕回到按文件解析出的样子。
func TestReparse(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	svc, pool := newBindingService(t, &fakeAdapter{}, slogTo(&logs))
	created, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1, snapshot2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `
		DELETE FROM danmaku WHERE binding_id = 1 AND source_id = 3;
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES (1, 4, 0, 1, 0, '旧规则解析出的');
		UPDATE bindings SET "offset" = 3 WHERE id = 1;`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Reparse(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.DanmakuCount != 3 || got.SourceLabel != "弹幕文件 · 2 份" || got.Offset != 3 {
		t.Errorf("Reparse() = %+v, want 3 条、2 份，偏移不变", got)
	}
	if items := readDanmaku(t, pool, created.ID); !slices.Equal(items, snapshotDanmaku) {
		t.Errorf("落库的弹幕 = %+v\nwant %+v", items, snapshotDanmaku)
	}
	if v := getBinding(t, pool, created.ID).ContentVersion; v != 2 {
		t.Errorf("content_version = %d, want 2", v)
	}
	if wantLog := `level=INFO msg="danmaku files reparsed" binding_id=1 files=2 added=3`; !strings.Contains(logs.String(), wantLog) {
		t.Errorf("日志 = %q, want 含 %q", logs.String(), wantLog)
	}
}

// TestReparseUnparsable 存下的文件按现在的规则解析不了：返回 422，弹幕不动。
func TestReparseUnparsable(t *testing.T) {
	t.Parallel()
	svc, pool := newBindingService(t, &fakeAdapter{}, slogTo(io.Discard))
	created, err := svc.CreateFromFiles(t.Context(), 1, []UploadedFile{snapshot1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE binding_files SET content = '<html></html>'`); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Reparse(t.Context(), created.ID)

	assertAppError(t, err, http.StatusUnprocessableEntity, "无法识别「20130709.xml」：目前只支持 B 站的 XML 弹幕文件，且文件要完整")
	if items := readDanmaku(t, pool, created.ID); len(items) != 2 {
		t.Errorf("弹幕 = %+v, want 原来的 2 条", items)
	}
}

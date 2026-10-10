package server

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"path"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// seasonUpload 按季上传：files 的 name 是以所选文件夹名开头的相对路径。与浏览器一样，字段 files 里的文件名只有 base name，
// 相对路径按相同顺序放进字段 paths（一个 JSON 数组）。
func seasonUpload(t *testing.T, srv *Server, target string, files []uploadFile, targets string, wantStatus int) (message string, data json.RawMessage) {
	t.Helper()
	paths := make([]string, len(files))
	parts := make([]uploadFile, len(files))
	for i, f := range files {
		paths[i] = f.name
		parts[i] = uploadFile{name: path.Base(f.name), data: f.data}
	}
	return uploadForm(t, srv, target, parts, map[string]string{"paths": jsonString(paths), "targets": targets}, wantStatus)
}

// TestSeasonUpload 按路径分成条目（子目录里的文件合成一个条目，顶层的文件各自一个条目，扩展名不分大小写），
// 每个条目连同它的文件交给 targets 指定的集；标题、存下的文件名、留下的季绑定等由 seasonbinding 的测试覆盖。
func TestSeasonUpload(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	_, data := seasonUpload(t, srv, "/api/seasons/1/file-bindings", []uploadFile{
		danmakuXML("星海旅人/1/20130709.xml", "1", "2"),
		danmakuXML("星海旅人/2.XML", "5"),
		danmakuXML("星海旅人/1/20130711.xml", "2", "3"),
	}, `[{"label": "星海旅人 / 1", "episodeId": 2}, {"label": "星海旅人 / 2", "episodeId": 1}]`, http.StatusCreated)
	testenv.AssertJSON(t, data, `{"bindings": 2, "added": 4}`)

	// 子目录 1 的两份文件合成一个绑定、到了第 2 集（弹幕 1、2、3），顶层的 2.XML 到了第 1 集
	if got, want := fileBindingCounts(t, srv, 1), map[int64][]int32{1: {1}, 2: {3}}; !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("各集用弹幕文件建的绑定的弹幕条数 = %v, want %v", got, want)
	}
}

// TestSeasonUploadErrors 上限、路径结构、条目与目标的对应不满足时整次 400，什么都不建。
func TestSeasonUploadErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)
	const target = "/api/seasons/1/file-bindings"
	const oneTarget = `[{"label": "星海旅人 / 1", "episodeId": 1}]`

	many := make([]uploadFile, 61)
	for i := range many {
		many[i] = danmakuXML("星海旅人/1/"+jsonString(i)+".xml", "1")
	}
	big := uploadFile{name: "星海旅人/1/大.xml", data: bytes.Repeat([]byte("a"), 10<<20+1)}
	nineMB := make([]uploadFile, 7)
	for i := range nineMB {
		nineMB[i] = uploadFile{name: "星海旅人/1/" + jsonString(i) + ".xml", data: bytes.Repeat([]byte("a"), 9<<20)}
	}

	for _, tt := range []struct {
		name        string
		target      string
		files       []uploadFile
		targets     string
		wantMessage string
	}{
		{"没有文件", target, nil, oneTarget, "请选择弹幕文件"},
		{"超过 60 份", target, many, oneTarget, "一次最多上传 60 份文件"},
		{"单份超过 10 MB", target, []uploadFile{big}, oneTarget, "「星海旅人/1/大.xml」超过 10 MB"},
		{"合计超过 60 MB", target, nineMB, oneTarget, "一次上传的文件合计不能超过 60 MB"},
		{
			"超过两层", target,
			[]uploadFile{danmakuXML("星海旅人/1/a/1.xml", "1")},
			oneTarget,
			"目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：星海旅人/1/a/1.xml",
		},
		{"没有文件夹", target, []uploadFile{danmakuXML("1.xml", "1")}, oneTarget, "文件路径不合法：1.xml"},
		{"不是 .xml", target, []uploadFile{danmakuXML("星海旅人/1/1.ass", "1")}, oneTarget, "只能上传 XML 弹幕文件：星海旅人/1/1.ass"},
		{
			"文件夹名不一致", target,
			[]uploadFile{danmakuXML("星海旅人/1/1.xml", "1"), danmakuXML("别的/2.xml", "1")},
			oneTarget,
			"文件要在同一个文件夹里：别的/2.xml",
		},
		{
			"条目名称重复", target,
			[]uploadFile{danmakuXML("星海旅人/1/a.xml", "1"), danmakuXML("星海旅人/1.xml", "1")},
			oneTarget,
			"条目名称重复：星海旅人 / 1",
		},
		{
			"条目没有目标", target,
			[]uploadFile{danmakuXML("星海旅人/1.xml", "1"), danmakuXML("星海旅人/2.xml", "1")},
			oneTarget,
			"「星海旅人 / 2」没有指定目标集",
		},
		{
			"目标没有文件", target,
			[]uploadFile{danmakuXML("星海旅人/1.xml", "1")},
			`[{"label": "星海旅人 / 1", "episodeId": 1}, {"label": "星海旅人 / 2", "episodeId": 2}]`,
			"「星海旅人 / 2」没有对应的文件",
		},
		{
			"条目对到多个目标", target,
			[]uploadFile{danmakuXML("星海旅人/1.xml", "1")},
			`[{"label": "星海旅人 / 1", "episodeId": 1}, {"label": "星海旅人 / 1", "episodeId": 2}]`,
			"「星海旅人 / 1」对应了多个目标集",
		},
		{"targets 不是 JSON", target, []uploadFile{danmakuXML("星海旅人/1.xml", "1")}, `[`, "请求参数错误"},
		{"季 ID 不合法", "/api/seasons/0/file-bindings", []uploadFile{danmakuXML("星海旅人/1.xml", "1")}, oneTarget, "季 ID 不合法"},
	} {
		if message, _ := seasonUpload(t, srv, tt.target, tt.files, tt.targets, http.StatusBadRequest); message != tt.wantMessage {
			t.Errorf("%s：message = %q, want %q", tt.name, message, tt.wantMessage)
		}
	}

	fields := map[string]string{"paths": `["星海旅人/1.xml", "星海旅人/2.xml"]`, "targets": `[{"label": "星海旅人 / 1", "episodeId": 1}, {"label": "星海旅人 / 2", "episodeId": 2}]`}
	if message, _ := uploadForm(t, srv, target, []uploadFile{danmakuXML("1.xml", "1")}, fields, http.StatusBadRequest); message != "文件路径与文件的份数不一致" {
		t.Errorf("paths 与 files 份数不一致：message = %q", message)
	}

	if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE kind = 'file'`); n != 0 {
		t.Errorf("建出了 %d 个用弹幕文件建的绑定", n)
	}
}

// fileBindingCounts 从剧详情里列出各集用弹幕文件建的绑定的弹幕条数，没有这种绑定的集不列出。
func fileBindingCounts(t *testing.T, srv *Server, seriesID int64) map[int64][]int32 {
	t.Helper()
	_, _, data := call(t, srv, http.MethodGet, "/api/series/"+jsonString(seriesID), "", http.StatusOK)
	var series struct {
		Seasons []struct {
			Episodes []struct {
				ID       int64 `json:"id"`
				Bindings []struct {
					Kind         string `json:"kind"`
					DanmakuCount int32  `json:"danmakuCount"`
				} `json:"bindings"`
			} `json:"episodes"`
		} `json:"seasons"`
	}
	if err := json.Unmarshal(data, &series); err != nil {
		t.Fatal(err)
	}
	counts := make(map[int64][]int32)
	for _, season := range series.Seasons {
		for _, ep := range season.Episodes {
			for _, b := range ep.Bindings {
				if b.Kind == "file" {
					counts[ep.ID] = append(counts[ep.ID], b.DanmakuCount)
				}
			}
		}
	}
	return counts
}

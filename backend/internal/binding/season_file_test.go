package binding_test

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// seasonFile 按季上传的一份文件：名称为相对路径，内容取 f 的。
func seasonFile(path string, f binding.UploadedFile) binding.UploadedFile {
	f.Name = path
	return f
}

// TestCreateFromSeasonFiles 每个条目在目标集上建出一个绑定：标题是条目名称，原文件按 base name 存下，同一条目的快照按原始 ID 去重。
func TestCreateFromSeasonFiles(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	svc, pool := newBindingService(t, &fakeAdapter{}, testenv.SlogTo(&logs))

	got, err := svc.CreateFromSeasonFiles(t.Context(), 1, []binding.SeasonEntry{
		{Label: "星海旅人 / 1", EpisodeID: 1, Files: []binding.UploadedFile{
			seasonFile("星海旅人/1/20130709.xml", snapshot1),
			seasonFile("星海旅人/1/20130711.xml", snapshot2),
		}},
		{Label: "星海旅人 / 2", EpisodeID: 2, Files: []binding.UploadedFile{
			seasonFile("星海旅人/2.xml", snapshot1),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := (binding.SeasonFilesCreated{Bindings: 2, Added: 5}); got != want {
		t.Errorf("CreateFromSeasonFiles() = %+v, want %+v", got, want)
	}
	views, err := svc.ListBySeries(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	first, second := views[1], views[2]
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("绑定 = %+v, want 两集各一个", views)
	}
	if b := first[0]; b.Kind != "file" || b.Title != "星海旅人 / 1" || b.SourceLabel != "弹幕文件 · 2 份" || b.DanmakuCount != 3 {
		t.Errorf("第 1 集的绑定 = %+v, want 标题为条目名称、2 份、3 条", b)
	}
	if b := second[0]; b.Title != "星海旅人 / 2" || b.SourceLabel != "弹幕文件 · 1 份" || b.DanmakuCount != 2 {
		t.Errorf("第 2 集的绑定 = %+v, want 标题为条目名称、1 份、2 条", b)
	}
	if items := readDanmaku(t, pool, first[0].ID); !slices.Equal(items, snapshotDanmaku) {
		t.Errorf("第 1 集落库的弹幕 = %+v\nwant %+v", items, snapshotDanmaku)
	}
	if names := fileNames(t, svc, first[0].ID); !slices.Equal(names, []string{"20130709.xml", "20130711.xml"}) {
		t.Errorf("第 1 集的弹幕文件 = %v", names)
	}
	if names := fileNames(t, svc, second[0].ID); !slices.Equal(names, []string{"2.xml"}) {
		t.Errorf("第 2 集的弹幕文件 = %v", names)
	}
	if wantLog := `level=INFO msg="season danmaku files added" season_id=1 bindings=2 files=3 added=5`; !strings.Contains(logs.String(), wantLog) {
		t.Errorf("日志 = %q, want 含 %q", logs.String(), wantLog)
	}
}

// TestCreateFromSeasonFilesExisting 目标集上已有用弹幕文件建的绑定时照常再建一个，已有的不变。
func TestCreateFromSeasonFilesExisting(t *testing.T) {
	t.Parallel()
	svc, _ := newBindingService(t, &fakeAdapter{}, testenv.Logger(t))
	existing, err := svc.CreateFromFiles(t.Context(), 1, []binding.UploadedFile{snapshot1})
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.CreateFromSeasonFiles(t.Context(), 1, []binding.SeasonEntry{
		{Label: "星海旅人 / 1", EpisodeID: 1, Files: []binding.UploadedFile{seasonFile("星海旅人/1.xml", snapshot1)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := (binding.SeasonFilesCreated{Bindings: 1, Added: 2}); got != want {
		t.Errorf("CreateFromSeasonFiles() = %+v, want %+v", got, want)
	}
	views, err := svc.ListBySeries(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if bs := views[1]; len(bs) != 2 || bs[0] != existing || bs[1].Title != "星海旅人 / 1" || bs[1].DanmakuCount != 2 {
		t.Errorf("第 1 集的绑定 = %+v, want 已有的不变、再建一个", bs)
	}
}

// TestCreateFromSeasonFilesErrors 任何一个条目出问题都整次失败，什么都不保存。
func TestCreateFromSeasonFilesErrors(t *testing.T) {
	t.Parallel()
	entry := func(label string, episodeID int64, files ...binding.UploadedFile) binding.SeasonEntry {
		return binding.SeasonEntry{Label: label, EpisodeID: episodeID, Files: files}
	}
	first := entry("星海旅人 / 1", 1, seasonFile("星海旅人/1/20130709.xml", snapshot1))
	tests := []struct {
		name     string
		seasonID int64
		entries  []binding.SeasonEntry
		status   int
		message  string
	}{
		{
			"有一份认不出：提示带着相对路径", 1,
			[]binding.SeasonEntry{first, entry("星海旅人 / 2", 2,
				seasonFile("星海旅人/2/20130711.xml", snapshot2), seasonFile("星海旅人/2/README.html", notDanmaku))},
			http.StatusUnprocessableEntity,
			"无法识别「星海旅人/2/README.html」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
		},
		{
			"目标集不属于这一季", 1,
			[]binding.SeasonEntry{first, entry("星海旅人 / 3", 3, seasonFile("星海旅人/3.xml", snapshot2))},
			http.StatusNotFound,
			"「星海旅人 / 3」的目标集已被删除或不属于这一季，请重新预览",
		},
		{
			"目标集已被删除", 1,
			[]binding.SeasonEntry{first, entry("星海旅人 / 9", 99, seasonFile("星海旅人/9.xml", snapshot2))},
			http.StatusNotFound,
			"「星海旅人 / 9」的目标集已被删除或不属于这一季，请重新预览",
		},
		{
			"两个条目对到同一集", 1,
			[]binding.SeasonEntry{first, entry("星海旅人 / 01", 1, seasonFile("星海旅人/01.xml", snapshot2))},
			http.StatusBadRequest,
			"「星海旅人 / 1」和「星海旅人 / 01」对到了同一集，请重新预览",
		},
		{"季不存在", 99, []binding.SeasonEntry{first}, http.StatusNotFound, "季不存在"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, pool := newBindingService(t, &fakeAdapter{}, testenv.Logger(t))
			// 另一季的一集（ID 3）
			if _, err := pool.Exec(t.Context(), `
				INSERT INTO seasons (series_id, number) VALUES (1, 2);
				INSERT INTO episodes (season_id, number, duration) VALUES (2, 1, 1420);`); err != nil {
				t.Fatal(err)
			}

			_, err := svc.CreateFromSeasonFiles(t.Context(), tt.seasonID, tt.entries)

			testenv.AssertAppError(t, err, tt.status, tt.message)
			assertNothingWritten(t, pool)
			if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM binding_files`); n != 0 {
				t.Errorf("存下了 %d 份文件", n)
			}
		})
	}
}

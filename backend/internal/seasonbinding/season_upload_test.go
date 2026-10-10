package seasonbinding_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// xmlFile 一份 B 站导出的 XML 弹幕文件，名称为在所选文件夹里的相对路径。ds 是 <d> 的 p 与正文交替排列。
func xmlFile(path string, ds ...string) binding.UploadedFile {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>934042</chatid>`)
	for i := 0; i < len(ds); i += 2 {
		b.WriteString(`<d p="` + ds[i] + `">` + ds[i+1] + "</d>\n")
	}
	b.WriteString("</i>")
	return binding.UploadedFile{Name: path, Data: []byte(b.String())}
}

// snapshot1、snapshot2 同一个视频不同日期的两份快照：dmid 2 两份都有，dmid 1、3 各在一份里。
func snapshot1(path string) binding.UploadedFile {
	return xmlFile(path,
		"1.5,1,25,16777215,1373250214,0,d9df08a7,1", "前排",
		"61.25,1,25,16777215,1373250228,0,500cea0d,2", "好看")
}

func snapshot2(path string) binding.UploadedFile {
	return xmlFile(path,
		"61.2499980927,1,25,16777215,1373250228,0,500cea0d,2", "好看",
		"120,5,25,16711680,1373250271,0,3ae9ab45,3", "顶部")
}

// uploadEntry 按季上传的一个条目。
func uploadEntry(label string, episodeID int64, files ...binding.UploadedFile) binding.SeasonEntry {
	return binding.SeasonEntry{Label: label, EpisodeID: episodeID, Files: files}
}

// upload 给第 1 季按季上传文件夹"来自新世界"，每个集 ID 一个条目"来自新世界 / 序号"（一份快照），返回留下的文件夹的季绑定的 ID。
func (e *seasonEnv) upload(episodeIDs ...int64) int64 {
	e.t.Helper()
	const folder = "来自新世界"
	entries := make([]binding.SeasonEntry, len(episodeIDs))
	for i, id := range episodeIDs {
		n := string(rune('1' + i))
		entries[i] = uploadEntry(folder+" / "+n, id, snapshot1(folder+"/"+n+".xml"))
	}
	if _, err := e.svc.CreateFromSeasonFiles(e.t.Context(), 1, folder, entries); err != nil {
		e.t.Fatalf("CreateFromSeasonFiles: %v", err)
	}
	views := e.seasonBindings()
	return views[len(views)-1].ID
}

// seasonBindings 剧详情里第 1 季的季绑定，按创建顺序。
func (e *seasonEnv) seasonBindings() []seasonbinding.View {
	e.t.Helper()
	views, err := e.svc.ListBySeries(e.t.Context(), 1)
	if err != nil {
		e.t.Fatalf("ListBySeries: %v", err)
	}
	return views[1]
}

// episodeBindings 剧详情里集 episodeID 上的绑定。
func (e *seasonEnv) episodeBindings(episodeID int64) []binding.View {
	e.t.Helper()
	views, err := e.bindingSvc.ListBySeries(e.t.Context(), 1)
	if err != nil {
		e.t.Fatalf("ListBySeries: %v", err)
	}
	return views[episodeID]
}

// fileNames 绑定里的弹幕文件名，按加入的顺序。
func (e *seasonEnv) fileNames(id int64) []string {
	e.t.Helper()
	files, err := e.bindingSvc.ListFiles(e.t.Context(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return names
}

// TestCreateFromSeasonFiles 按季上传留下一个文件夹的季绑定，名称为文件夹名；每个条目在目标集上建出一个指向它的文件绑定：
// 标题是条目名称，原文件按 base name 存下，同一条目的快照按原始 ID 去重。
func TestCreateFromSeasonFiles(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)

		got, err := env.svc.CreateFromSeasonFiles(t.Context(), 1, "星海旅人", []binding.SeasonEntry{
			uploadEntry("星海旅人 / 1", 1, snapshot1("星海旅人/1/20130709.xml"), snapshot2("星海旅人/1/20130711.xml")),
			uploadEntry("星海旅人 / 2", 2, snapshot1("星海旅人/2.xml")),
		})
		if err != nil {
			t.Fatal(err)
		}

		if want := (seasonbinding.SeasonFilesCreated{Bindings: 2, Added: 5}); got != want {
			t.Errorf("CreateFromSeasonFiles() = %+v, want %+v", got, want)
		}
		sbs := env.seasonBindings()
		if len(sbs) != 1 {
			t.Fatalf("季绑定 = %+v, want 一个文件夹的季绑定", sbs)
		}
		sb := sbs[0]
		if sb.Kind != "folder" || sb.Title != "星海旅人" || sb.BindingCount != 2 || sb.Follow {
			t.Errorf("季绑定 = %+v, want 文件夹的季绑定「星海旅人」、两个绑定、不追更", sb)
		}
		first, second := env.episodeBindings(1), env.episodeBindings(2)
		if len(first) != 1 || len(second) != 1 {
			t.Fatalf("绑定 = %+v、%+v, want 两集各一个", first, second)
		}
		for _, b := range []binding.View{first[0], second[0]} {
			if b.SeasonBindingID == nil || *b.SeasonBindingID != sb.ID {
				t.Errorf("绑定 %q 的季绑定 = %v, want %d", b.Title, b.SeasonBindingID, sb.ID)
			}
		}
		if b := first[0]; b.Kind != "file" || b.Title != "星海旅人 / 1" || b.SourceLabel != "弹幕文件 · 2 份" || b.DanmakuCount != 3 {
			t.Errorf("第 1 集的绑定 = %+v, want 标题为条目名称、2 份、3 条", b)
		}
		if b := second[0]; b.Title != "星海旅人 / 2" || b.SourceLabel != "弹幕文件 · 1 份" || b.DanmakuCount != 2 {
			t.Errorf("第 2 集的绑定 = %+v, want 标题为条目名称、1 份、2 条", b)
		}
		testenv.AssertStrings(t, "第 1 集的弹幕文件", env.fileNames(first[0].ID), []string{"20130709.xml", "20130711.xml"})
		testenv.AssertStrings(t, "第 2 集的弹幕文件", env.fileNames(second[0].ID), []string{"2.xml"})
		wantLog := `level=INFO msg="season danmaku files added" season_id=1 season_binding_id=1 bindings=2 files=3 added=5`
		if logs := env.logs.String(); !strings.Contains(logs, wantLog) {
			t.Errorf("日志 = %q, want 含 %q", logs, wantLog)
		}
	})
}

// TestCreateFromSeasonFilesExisting 目标集上已有用弹幕文件建的绑定时照常再建一个，已有的不变。
func TestCreateFromSeasonFilesExisting(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)
		existing, err := env.bindingSvc.CreateFromFiles(t.Context(), 1, []binding.UploadedFile{snapshot1("20130709.xml")})
		if err != nil {
			t.Fatal(err)
		}

		got, err := env.svc.CreateFromSeasonFiles(t.Context(), 1, "星海旅人", []binding.SeasonEntry{
			uploadEntry("星海旅人 / 1", 1, snapshot1("星海旅人/1.xml")),
		})
		if err != nil {
			t.Fatal(err)
		}

		if want := (seasonbinding.SeasonFilesCreated{Bindings: 1, Added: 2}); got != want {
			t.Errorf("CreateFromSeasonFiles() = %+v, want %+v", got, want)
		}
		if bs := env.episodeBindings(1); len(bs) != 2 || bs[0] != existing || bs[1].Title != "星海旅人 / 1" || bs[1].DanmakuCount != 2 {
			t.Errorf("第 1 集的绑定 = %+v, want 已有的不变、再建一个", bs)
		}
	})
}

// TestCreateFromSeasonFilesTwice 同一个文件夹传两次留下两个季绑定，各自记着自己建出的绑定。
func TestCreateFromSeasonFilesTwice(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)

		first := env.upload(1, 2)
		second := env.upload(2)

		var got []string
		for _, v := range env.seasonBindings() {
			got = append(got, v.Kind+" "+v.Title)
		}
		testenv.AssertStrings(t, "季绑定", got, []string{"folder 来自新世界", "folder 来自新世界"})
		if first == second {
			t.Fatalf("两次上传留下的是同一个季绑定 %d", first)
		}
		if d := env.get(first); d.BindingCount != 2 {
			t.Errorf("第一次的季绑定建出的绑定数 = %d, want 2", d.BindingCount)
		}
		if d := env.get(second); d.BindingCount != 1 {
			t.Errorf("第二次的季绑定建出的绑定数 = %d, want 1", d.BindingCount)
		}
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{
			"1 来自新世界 / 1 1", "2 来自新世界 / 2 1", "2 来自新世界 / 1 2",
		})
	})
}

// TestCreateFromSeasonFilesErrors 任何一个条目出问题都整次失败，既不留下季绑定，也不建出绑定。
func TestCreateFromSeasonFilesErrors(t *testing.T) {
	t.Parallel()
	first := uploadEntry("星海旅人 / 1", 1, snapshot1("星海旅人/1/20130709.xml"))
	notDanmaku := binding.UploadedFile{Name: "星海旅人/2/README.html", Data: []byte("<html><body>历史弹幕</body></html>")}
	tests := []struct {
		name     string
		seasonID int64
		entries  []binding.SeasonEntry
		status   int
		message  string
	}{
		{
			"有一份认不出：提示带着相对路径", 1,
			[]binding.SeasonEntry{first, uploadEntry("星海旅人 / 2", 2, snapshot2("星海旅人/2/20130711.xml"), notDanmaku)},
			http.StatusUnprocessableEntity,
			"无法识别「星海旅人/2/README.html」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
		},
		{
			"目标集不属于这一季", 1,
			[]binding.SeasonEntry{first, uploadEntry("星海旅人 / 3", 3, snapshot2("星海旅人/3.xml"))},
			http.StatusNotFound,
			"「星海旅人 / 3」的目标集已被删除或不属于这一季，请重新预览",
		},
		{
			"目标集已被删除", 1,
			[]binding.SeasonEntry{first, uploadEntry("星海旅人 / 9", 99, snapshot2("星海旅人/9.xml"))},
			http.StatusNotFound,
			"「星海旅人 / 9」的目标集已被删除或不属于这一季，请重新预览",
		},
		{
			"两个条目对到同一集", 1,
			[]binding.SeasonEntry{first, uploadEntry("星海旅人 / 01", 1, snapshot2("星海旅人/01.xml"))},
			http.StatusBadRequest,
			"「星海旅人 / 1」和「星海旅人 / 01」对到了同一集，请重新预览",
		},
		{"季不存在", 99, []binding.SeasonEntry{first}, http.StatusNotFound, "季不存在"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)
				env.exec(`INSERT INTO episodes (season_id, number, duration) VALUES (2, 1, 1420)`) // 另一季的一集（ID 3）

				_, err := env.svc.CreateFromSeasonFiles(t.Context(), tt.seasonID, "星海旅人", tt.entries)

				testenv.AssertAppError(t, err, tt.status, tt.message)
				for _, table := range []string{"season_bindings", "bindings", "binding_files", "danmaku"} {
					if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM `+table); n != 0 {
						t.Errorf("%s 里有 %d 行，want 没有", table, n)
					}
				}
			})
		})
	}
}

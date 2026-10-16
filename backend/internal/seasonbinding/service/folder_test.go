package service_test

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// folderForm 文件夹的表单：第 1 集为本地第 1 集，默认的集号规则。
func folderForm() service.FolderForm {
	return service.FolderForm{Mapping: source.Mapping{From: 1, To: 1}, Rule: source.DefaultEpisodeRule()}
}

// createFolder 用上传的文件给第 1 季创建文件夹的季绑定，返回详情。
func (e *seasonEnv) createFolder(files ...danmakufile.File) service.Detail {
	e.t.Helper()
	d, err := e.svc.CreateFolder(e.t.Context(), 1, files, folderForm())
	if err != nil {
		e.t.Fatalf("CreateFolder: %v", err)
	}
	return d
}

// TestCreateFolder 创建：按路径分成条目（子目录里的文件合成一个条目，顶层的文件各自一个条目），条目名称不带文件夹名，
// 按"文件夹名 / 条目名称"认集号；对到集的当场建出文件绑定（标题为条目名称，弹幕是条目各份文件合并去重的结果），
// 对不上的条目也保存。
func TestCreateFolder(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)
		d := env.createFolder(
			testenv.Snapshot1("星海旅人/01.xml"),
			testenv.Snapshot1("星海旅人/02/a.xml"),
			testenv.Snapshot2("星海旅人/02/b.XML"),
			testenv.Snapshot1("星海旅人/花絮.xml"),
		)
		if d.Kind != "folder" || d.Title != "星海旅人" || d.EntryCount != 3 || d.BindingCount != 2 {
			t.Errorf("CreateFolder() = %+v, want 标题为文件夹名、3 个条目、2 个绑定", d.View)
		}
		testutil.AssertStrings(t, "条目", states(d), []string{
			"星海旅人 / 01 keep 1", "星海旅人 / 02 keep 2", "星海旅人 / 花絮 unmatched - 不符合集号规则",
		})
		testutil.AssertStrings(t, "绑定", env.bindings(), []string{"1 01 1", "2 02 1"})
		if n := testenv.QueryInt(t, pool, `SELECT danmaku_count FROM bindings WHERE title = '02'`); n != 3 {
			t.Errorf("条目 02 的绑定有 %d 条弹幕, want 3（两份快照合并去重）", n)
		}
	})
}

// saveFolder 保存文件夹的季绑定的表单：files 为空时不换文件夹。
func (e *seasonEnv) saveFolder(id int64, f service.FolderForm, files ...danmakufile.File) service.Detail {
	e.t.Helper()
	d, err := e.svc.SaveFolder(e.t.Context(), id, files, f)
	if err != nil {
		e.t.Fatalf("SaveFolder: %v", err)
	}
	return d
}

// slots 建出的绑定："集号 标题 #绑定 ID 偏移"，按集号排序。
func (e *seasonEnv) slots() []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `
		SELECT format('%s %s #%s %s', e.number, b.title, b.id, b."offset")
		FROM bindings b
		JOIN episodes e ON e.id = b.episode_id
		WHERE b.season_binding_id IS NOT NULL
		ORDER BY e.number`)
	if err != nil {
		e.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// TestSaveFolder 保存（不换文件夹）：没变的绑定不动，偏移保留；改了集号对应之后，对到别的集的条目替换那一集的槽位
// （删旧建新、偏移归零），没有条目的槽位删掉，对到目录里没有的集的条目不建。
func TestSaveFolder(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2, 3)
		id := env.createFolder(
			testenv.Snapshot1("星海旅人/01.xml"), testenv.Snapshot1("星海旅人/02.xml"), testenv.Snapshot1("星海旅人/03.xml"),
		).ID
		env.exec(`UPDATE bindings SET "offset" = 2.5`)
		before := env.slots()
		testutil.AssertStrings(t, "创建后的绑定", before, []string{"1 01 #1 2.5", "2 02 #2 2.5", "3 03 #3 2.5"})

		d := env.saveFolder(id, folderForm())
		testutil.AssertStrings(t, "原样保存后的绑定", env.slots(), before)
		testutil.AssertStrings(t, "条目", states(d), []string{"星海旅人 / 01 keep 1", "星海旅人 / 02 keep 2", "星海旅人 / 03 keep 3"})

		f := folderForm()
		f.Mapping = source.Mapping{From: 1, To: 2}
		d = env.saveFolder(id, f)
		testutil.AssertStrings(t, "改了集号对应后的绑定", env.slots(), []string{"2 01 #4 0", "3 02 #5 0"})
		testutil.AssertStrings(t, "条目", states(d), []string{"星海旅人 / 01 keep 2", "星海旅人 / 02 keep 3", "星海旅人 / 03 noEpisode 4"})
		testutil.AssertStrings(t, "没有条目的槽位", emptySlots(d), nil)
		if d.MappingTo != 2 {
			t.Errorf("保存后的集号对应 = %d = %d", d.MappingFrom, d.MappingTo)
		}
	})
}

// picked 预览时选的文件夹：各条目的名称和文件哈希取自要上传的文件，与 groupFolder 同样按路径分组。
func picked(folder string, files ...danmakufile.File) *service.PickedFolder {
	p := &service.PickedFolder{Folder: folder}
	index := map[string]int{}
	for _, f := range files {
		label := strings.TrimSuffix(strings.Split(strings.TrimPrefix(f.Name, folder+"/"), "/")[0], ".xml")
		i, ok := index[label]
		if !ok {
			i = len(p.Entries)
			index[label] = i
			p.Entries = append(p.Entries, service.EntryHashes{Label: label})
		}
		p.Entries[i].SHA256s = append(p.Entries[i].SHA256s, sha256.Sum256(f.Data))
	}
	return p
}

// TestChangeFolder 换文件夹：预览和保存一致；名称和文件哈希都没变的条目沿用（绑定不动、偏移保留），文件夹改名后照样对得上；
// 文件变了的条目重建，替换原来的槽位；不在新文件夹里的条目删掉，它的槽位随之删除；标题改为新的文件夹名。
func TestChangeFolder(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2, 3)
		id := env.createFolder(
			testenv.Snapshot1("星海旅人/01.xml"), testenv.Snapshot1("星海旅人/02.xml"), testenv.Snapshot1("星海旅人/03.xml"),
		).ID
		env.exec(`UPDATE bindings SET "offset" = 2.5`)
		files := []danmakufile.File{
			testenv.Snapshot1("星海旅人 BD/01.xml"), testenv.Snapshot2("星海旅人 BD/02.xml"), testenv.Snapshot1("星海旅人 BD/04.xml"),
		}

		p, err := env.svc.PreviewFolderEdit(t.Context(), id, picked("星海旅人 BD", files...), folderForm())
		if err != nil {
			t.Fatalf("PreviewFolderEdit: %v", err)
		}
		c := p.Candidates[0]
		testutil.AssertStrings(t, "预览的条目", states(service.Detail{Items: c.Items}), []string{
			"星海旅人 BD / 01 keep 1", "星海旅人 BD / 02 replace 2 替换 02", "星海旅人 BD / 04 noEpisode 4",
		})
		testutil.AssertJSON(t, c.Removed, `[{"episodeNumber": 3, "bindingId": 3, "title": "03", "dead": false}]`)
		testutil.AssertJSON(t, c.Summary, `{"create": 0, "replace": 1, "remove": 1, "keep": 1}`)
		if c.Title != "星海旅人 BD" || !c.Current || c.CollectionKind != nil {
			t.Errorf("预览的一组 = %+v", c)
		}

		d := env.saveFolder(id, folderForm(), files...)
		if d.Title != "星海旅人 BD" || d.EntryCount != 3 {
			t.Errorf("换文件夹后的季绑定 = %+v, want 标题为新的文件夹名、3 个条目", d.View)
		}
		testutil.AssertStrings(t, "条目", states(d), []string{
			"星海旅人 BD / 01 keep 1", "星海旅人 BD / 02 keep 2", "星海旅人 BD / 04 noEpisode 4",
		})
		testutil.AssertStrings(t, "绑定", env.slots(), []string{"1 01 #1 2.5", "2 02 #4 0"})
		if got := testenv.FileNames(t, env.bindingSvc, 4); len(got) != 1 {
			t.Errorf("重建的绑定的文件 = %q", got)
		}
	})
}

// TestBackfillFolder 立即补建：按保存的表单当场新建、替换槽位，不删除没有条目的槽位。
func TestBackfillFolder(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2, 3)
		id := env.createFolder(
			testenv.Snapshot1("星海旅人/01.xml"), testenv.Snapshot1("星海旅人/02.xml"), testenv.Snapshot1("星海旅人/03.xml"),
		).ID
		env.exec(`UPDATE bindings SET "offset" = 2.5`)
		// 第 3 集的绑定没了，集号对应改成第 1 集为本地第 2 集：01 → 2、02 → 3、03 → 4（目录里没有）
		env.exec(`DELETE FROM bindings WHERE title = '03'`)
		env.exec(`UPDATE season_bindings SET mapping_to = 2`)

		d, err := env.svc.Backfill(t.Context(), id)
		if err != nil {
			t.Fatalf("Backfill: %v", err)
		}
		testutil.AssertStrings(t, "绑定", env.slots(), []string{"1 01 #1 2.5", "2 01 #4 0", "3 02 #5 0"})
		testutil.AssertStrings(t, "条目", states(d), []string{
			"星海旅人 / 01 keep 2", "星海旅人 / 02 keep 3", "星海旅人 / 03 noEpisode 4",
		})
		testutil.AssertStrings(t, "没有条目的槽位", emptySlots(d), []string{"1 01"})
	})
}

// TestCreateFolderRejected 有一份文件解析失败时整次 422（提示带着它的相对路径），文件夹的结构不对时 400，什么都不保存。
func TestCreateFolderRejected(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)
		_, err := env.svc.CreateFolder(t.Context(), 1, []danmakufile.File{
			testenv.Snapshot1("星海旅人/01.xml"),
			{Name: "星海旅人/02/坏.xml", Data: []byte("<i><d")},
		}, folderForm())
		testutil.AssertAppError(t, err, http.StatusUnprocessableEntity,
			"无法识别「星海旅人/02/坏.xml」：目前只支持 B 站的 XML 弹幕文件，且文件要完整")

		_, err = env.svc.CreateFolder(t.Context(), 1, []danmakufile.File{
			testenv.Snapshot1("星海旅人/01.xml"), testenv.Snapshot1("星海旅人/01/a.xml"),
		}, folderForm())
		testutil.AssertAppError(t, err, http.StatusBadRequest, "条目名称重复：01")

		if n := testenv.QueryInt(t, pool, `SELECT (SELECT count(*) FROM season_bindings) + (SELECT count(*) FROM bindings)`); n != 0 {
			t.Errorf("保存了 %d 个季绑定和绑定", n)
		}
	})
}

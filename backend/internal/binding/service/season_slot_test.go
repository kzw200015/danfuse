package service_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// linkSlot 季绑定 sb 在集 episodeID 上建出弹幕源 name 的链接绑定，拉取结果为 video。
func linkSlot(sb, episodeID int64, name string) service.LinkSlot {
	return service.LinkSlot{
		SeasonBindingID: sb, EpisodeID: episodeID, Adapter: "fake", Ref: source.Ref(`{"name": "` + name + `"}`),
		Fetched: video, CreatedAt: time.Now(),
	}
}

// TestLinkSlot 季绑定建出链接绑定：带着季绑定 ID；集上已有同一个弹幕源时 ErrSourceBound、集已被删除时 ErrEpisodeGone，
// 都什么都不写；删除槽位连同弹幕删掉，别的槽位不动。建出的绑定不能单独删除（409），偏移照常能改。
func TestLinkSlot(t *testing.T) {
	t.Parallel()
	svc, pool := newBindingService(t, &fakeAdapter{sources: map[string]source.Fetched{"m": video}}, testenv.Logger(t))
	sb := testenv.InsertCollectionSeasonBinding(t, pool, 1, "s", 1, 1)
	other := testenv.InsertCollectionSeasonBinding(t, pool, 1, "t", 1, 1)

	var saved service.Saved
	err := testenv.InTx(t, pool, func(tx pgx.Tx) error {
		var err error
		saved, err = svc.CreateLinkSlotInTx(t.Context(), tx, linkSlot(sb, 1, "a"))
		return err
	})
	if err != nil {
		t.Fatalf("CreateLinkSlotInTx: %v", err)
	}
	svc.LogFetched(t.Context(), saved)
	if saved.Added != 3 {
		t.Errorf("Added = %d, want 3", saved.Added)
	}
	b := getBinding(t, pool, 1)
	if b.SeasonBindingID == nil || *b.SeasonBindingID != sb || b.Title != video.Title || b.EntryID != nil {
		t.Errorf("建出的绑定 = %+v", b)
	}

	// 别的季绑定、手动绑定撞上同一个弹幕源；集已被删除
	manual, err := svc.Create(t.Context(), 2, "fake/m")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		slot service.LinkSlot
		want error
	}{
		{"别的季绑定建出的同源", linkSlot(other, 1, "a"), service.ErrSourceBound},
		{"手动绑定的同源", linkSlot(sb, 2, "m"), service.ErrSourceBound},
		{"集已被删除", linkSlot(sb, 9, "b"), service.ErrEpisodeGone},
	} {
		err := testenv.InTx(t, pool, func(tx pgx.Tx) error {
			_, err := svc.CreateLinkSlotInTx(t.Context(), tx, tt.slot)
			return err
		})
		if !errors.Is(err, tt.want) {
			t.Errorf("%s：err = %v, want %v", tt.name, err, tt.want)
		}
	}
	if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings`); n != 2 {
		t.Errorf("有 %d 个绑定，want 2", n)
	}

	testutil.AssertAppError(t, svc.Delete(t.Context(), b.ID), http.StatusConflict,
		"这个绑定由季绑定「合集 s」建出，不能单独删除或追加文件；要改，去季面板编辑季绑定")
	if _, err := svc.UpdateOffset(t.Context(), b.ID, 2); err != nil {
		t.Errorf("UpdateOffset: %v", err)
	}

	// 删除槽位：只删这个季绑定在这些集上的
	err = testenv.InTx(t, pool, func(tx pgx.Tx) error {
		if _, err := svc.CreateLinkSlotInTx(t.Context(), tx, linkSlot(other, 2, "c")); err != nil {
			return err
		}
		return svc.DeleteSlotsInTx(t.Context(), tx, sb, []int64{1, 2})
	})
	if err != nil {
		t.Fatalf("DeleteSlotsInTx: %v", err)
	}
	got := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE id = $1`, b.ID)
	rows := testenv.QueryInt(t, pool, `SELECT count(*) FROM danmaku WHERE binding_id = $1`, b.ID)
	left := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE id = $1 OR season_binding_id = $2`, manual.ID, other)
	if got != 0 || rows != 0 || left != 2 {
		t.Errorf("删除槽位之后：绑定 %d 个、弹幕 %d 条、手动绑定和别的季绑定的槽位 %d 个，want 0、0、2", got, rows, left)
	}
}

// TestFileSlot 文件夹的季绑定按条目建出文件绑定：标题是条目名称，弹幕文件是条目的（没有 binding_files），标签写条目的份数；
// 查看文件、重新解析读条目的文件。不能单独删除、追加文件（409）。集已被删除时 ErrEpisodeGone。
func TestFileSlot(t *testing.T) {
	t.Parallel()
	svc, pool := newBindingService(t, &fakeAdapter{}, testenv.Logger(t))
	sb, entries := testenv.InsertFolderSeasonBinding(t, pool, 1, "星海旅人", 1, 1,
		testenv.FolderEntry{Label: "第1话", Files: []danmakufile.File{snapshot1, snapshot2}})
	var items []danmaku.Danmaku
	for _, f := range []danmakufile.File{snapshot1, snapshot2} {
		parsed, err := danmakufile.Parse(f.Name, f.Data)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, parsed...)
	}
	slot := service.FileSlot{SeasonBindingID: sb, EpisodeID: 1, EntryID: entries[0], Title: "第1话", Danmaku: items}

	if err := testenv.InTx(t, pool, func(tx pgx.Tx) error { return svc.CreateFileSlotInTx(t.Context(), tx, slot) }); err != nil {
		t.Fatalf("CreateFileSlotInTx: %v", err)
	}
	views, err := svc.ListBySeries(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	v := views[1][0]
	if v.Kind != "file" || v.Title != "第1话" || v.SourceLabel != "弹幕文件 · 2 份" || v.DanmakuCount != 3 || v.SeasonBindingID == nil || *v.SeasonBindingID != sb {
		t.Errorf("建出的文件绑定 = %+v", v)
	}
	if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM binding_files`); n != 0 {
		t.Errorf("binding_files 有 %d 行，want 0", n)
	}
	if names := testenv.FileNames(t, svc, v.ID); !slices.Equal(names, []string{"20130709.xml", "20130711.xml"}) {
		t.Errorf("弹幕文件 = %q", names)
	}

	if _, err := pool.Exec(t.Context(), `DELETE FROM danmaku WHERE source_id = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE bindings SET danmaku_count = 2`); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Reparse(t.Context(), v.ID)
	if err != nil {
		t.Fatalf("Reparse: %v", err)
	}
	if got.DanmakuCount != 3 || got.SourceLabel != "弹幕文件 · 2 份" {
		t.Errorf("Reparse() = %+v, want 3 条、2 份", got)
	}
	if items := readDanmaku(t, pool, v.ID); !slices.Equal(items, snapshotDanmaku) {
		t.Errorf("重新解析之后的弹幕 = %+v\nwant %+v", items, snapshotDanmaku)
	}

	const message = "这个绑定由季绑定「星海旅人」建出，不能单独删除或追加文件；要改，去季面板编辑季绑定"
	testutil.AssertAppError(t, svc.Delete(t.Context(), v.ID), http.StatusConflict, message)
	_, err = svc.AppendFiles(t.Context(), v.ID, []danmakufile.File{snapshot1})
	testutil.AssertAppError(t, err, http.StatusConflict, message)

	slot.EpisodeID = 9
	err = testenv.InTx(t, pool, func(tx pgx.Tx) error { return svc.CreateFileSlotInTx(t.Context(), tx, slot) })
	if !errors.Is(err, service.ErrEpisodeGone) {
		t.Errorf("集已被删除：err = %v, want ErrEpisodeGone", err)
	}
}

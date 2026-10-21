package service_test

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func seasonRule(t *testing.T, second bool) source.EpisodeRule {
	t.Helper()
	c := source.DefaultEpisodeRuleV2().Config()
	c.Rules = nil
	for i := 1; i <= 2; i++ {
		if i == 2 && !second {
			break
		}
		p, err := source.RenderEpisodeTemplate("seasonNumber", &i, int64((i-1)*12))
		if err != nil {
			t.Fatal(err)
		}
		c.Rules = append(c.Rules, p)
	}
	r, err := source.ParseEpisodeRuleConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Saving a season-two override preserves all twelve existing slots and their data.
func TestEpisodeRulesCrossSeasonSaveAndBackfill(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		const parent = "【我心里危险的东西 全两季】全25集+特别篇 超清中字（未删减版）"
		col := source.Collection{Title: parent, NumberedByRule: true}
		var names []string
		for i := 1; i <= 25; i++ {
			s, n := "一", i
			if i > 12 {
				s, n = "二", i-12
			}
			name, label := fmt.Sprintf("ep%d", i), fmt.Sprintf("第%s季%02d", s, n)
			it := entry(name, 0)
			it.Label, it.LabelParts = parent+source.LabelSeparator+label, []string{parent, label}
			col.Items = append(col.Items, it)
			names = append(names, name)
		}
		sp := entry("special", 0)
		sp.Label, sp.LabelParts = parent+" / 特别篇", []string{parent, "特别篇"}
		col.Items = append(col.Items, sp)
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": col}, Videos: testenv.FakeVideos(names...)}
		episodes := make([]int, 25)
		for i := range episodes {
			episodes[i] = i + 1
		}
		env := newSeasonEnv(t, pool, src, episodes...)
		form := collectionForm("list/s")
		form.Rule = seasonRule(t, false)
		d, err := env.svc.CreateCollection(t.Context(), 1, form)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		env.exec(`UPDATE bindings SET "offset" = 2.5 WHERE season_binding_id = $1`, d.ID)
		snapshot := func() []byte {
			var b []byte
			err := pool.QueryRow(t.Context(), `
				SELECT json_agg(r ORDER BY r.id) FROM (
					SELECT b.id, b.episode_id, b."offset", b.danmaku_count, b.content_version,
						(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.source_id) FROM danmaku d WHERE d.binding_id = b.id) AS danmaku
					FROM bindings b JOIN episodes e ON e.id = b.episode_id
					WHERE b.season_binding_id = $1 AND e.number <= 12
				) r`, d.ID).Scan(&b)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		before := snapshot()
		form.Rule = seasonRule(t, true)
		p, err := env.svc.PreviewCollectionEdit(t.Context(), d.ID, form)
		if err != nil {
			t.Fatal(err)
		}
		c := p.Candidates[0]
		if c.Summary != (service.Summary{Keep: 12, Create: 13}) {
			t.Fatalf("preview summary = %+v", c.Summary)
		}
		if it := c.Items[20]; it.EpisodeNumber == nil || *it.EpisodeNumber != 21 || it.Trace.Season != "第二季" || it.Trace.Adjustment != 12 {
			t.Fatalf("second season 09 = %+v", it)
		}
		if c.Items[25].ReasonCode != source.ReasonNonRegular {
			t.Fatal("special not excluded")
		}
		if _, err := env.svc.SaveCollection(t.Context(), d.ID, form); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		got := env.get(d.ID)
		if got.BindingCount != 25 || !reflect.DeepEqual(got.EpisodeRule, form.Rule.Config()) || !bytes.Equal(before, snapshot()) {
			t.Fatalf("save changed existing slots or config: %+v", got.View)
		}
		for i, it := range got.Items[:25] {
			if it.Status != service.StatusKeep || it.EpisodeNumber == nil || *it.EpisodeNumber != i+1 {
				t.Fatalf("detail item %d = %+v", i, it)
			}
		}
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_binding_items WHERE label_parts IS NOT NULL AND number IS NULL`); n != 26 {
			t.Fatalf("structured titles/platform cache: %d", n)
		}
		env.backfill(d.ID)
		if !bytes.Equal(before, snapshot()) || len(src.FetchedNames()) != 25 {
			t.Fatal("backfill reapplied adjustment or refetched retained slots")
		}
		_, err = env.svc.SaveCollection(t.Context(), d.ID, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusConflict, "此绑定已使用新版集号规则，请刷新客户端后重试")
		_, err = env.svc.CreateCollection(t.Context(), 1, form)
		testutil.AssertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")

		env.exec(`UPDATE season_binding_items SET label_parts = NULL WHERE position = 1`)
		if it := env.get(d.ID).Items[0]; it.ReasonCode != source.ReasonMissingTitle {
			t.Fatalf("historical cache guessed hierarchy: %+v", it)
		}
		env.backfill(d.ID)
		if it := env.get(d.ID).Items[0]; it.Status != service.StatusKeep {
			t.Fatalf("backfill did not refresh title: %+v", it)
		}
	})
}

func TestEpisodeRulesFolderParity(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 13, 21)
		files := []danmakufile.File{
			testenv.Snapshot1("全25集+特别篇/第一季01.xml"),
			testenv.Snapshot1("全25集+特别篇/第二季01.xml"),
			testenv.Snapshot1("全25集+特别篇/第二季09.xml"),
			testenv.Snapshot1("全25集+特别篇/特别篇.xml"),
		}
		f := service.FolderForm{Mapping: source.Mapping{From: 1, To: 1}, Rule: seasonRule(t, true)}
		picked := service.PickedFolder{Folder: "全25集+特别篇"}
		for _, label := range []string{"第一季01", "第二季01", "第二季09", "特别篇"} {
			picked.Entries = append(picked.Entries, service.EntryHashes{Label: label})
		}
		p, err := env.svc.PreviewNewFolder(t.Context(), 1, picked, f)
		if err != nil {
			t.Fatal(err)
		}
		if p.Candidates[0].Summary.Create != 3 || p.Candidates[0].Items[3].ReasonCode != source.ReasonNonRegular {
			t.Fatalf("folder preview = %+v", p)
		}
		d, err := env.svc.CreateFolder(t.Context(), 1, files, f)
		if err != nil {
			t.Fatal(err)
		}
		env.exec(`UPDATE bindings SET "offset" = 3 WHERE season_binding_id = $1`, d.ID)
		before := env.bindings()
		if _, err := env.svc.SaveFolder(t.Context(), d.ID, nil, f); err != nil {
			t.Fatal(err)
		}
		env.backfill(d.ID)
		d = env.get(d.ID)
		if d.BindingCount != 3 || !reflect.DeepEqual(before, env.bindings()) || *d.Items[2].EpisodeNumber != 21 {
			t.Fatalf("folder save/backfill = %+v", d)
		}
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE "offset" = 3`); n != 3 {
			t.Fatal("retained file slots lost offsets")
		}
		_, err = env.svc.SaveFolder(t.Context(), d.ID, nil, service.FolderForm{Mapping: f.Mapping, Rule: source.DefaultEpisodeRule()})
		testutil.AssertAppError(t, err, http.StatusConflict, "此绑定已使用新版集号规则，请刷新客户端后重试")
	})
}

func TestEpisodeRulesMappingRange(t *testing.T) {
	r := service.Derive([]service.Entry{{
		Source: "a", Label: "1", Number: 1, Trace: &source.EpisodeTrace{Version: 2},
	}}, source.Mapping{From: 0, To: source.MaxEpisodeNumber}, nil)
	if it := r.Items[0]; it.Status != service.StatusUnmatched || it.Entry.ReasonCode != source.ReasonMapping ||
		it.Entry.Trace.LocalNumber == nil || *it.Entry.Trace.LocalNumber != int64(source.MaxEpisodeNumber)+1 {
		t.Fatalf("mapping overflow = %+v", it)
	}
}

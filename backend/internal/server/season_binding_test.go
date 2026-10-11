package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// 假适配器的合集能力：链接 "fakelist/<名字>" 识别为一个种类为 list 的候选 {"list":"<名字>"}。
// ListCollection 对名字 gone 返回 NotFound、对 down 返回 Upstream；其余返回三个条目：
// 序号为 1 的 <名字>1、序号为 2 的 <名字>2、平台没给出整数集号的 <名字>sp，弹幕源 ref 与贴链接 "fake/<条目名>" 相同。

type fakeListRef struct {
	List string `json:"list"`
}

func (fakeAdapter) ParseCollectionLink(_ context.Context, link string) ([]source.CollectionCandidate, error) {
	name, ok := strings.CutPrefix(link, "fakelist/")
	if !ok {
		return nil, source.ErrUnrecognized
	}
	ref, err := json.Marshal(fakeListRef{List: name})
	return []source.CollectionCandidate{{Kind: "list", Ref: ref}}, err
}

func (fakeAdapter) ListCollection(_ context.Context, ref source.CollectionRef) (source.Collection, error) {
	var r fakeListRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Collection{}, err
	}
	switch r.List {
	case "gone":
		return source.Collection{}, &source.Error{Kind: source.NotFound, Message: "合集不存在或已删除"}
	case "down":
		return source.Collection{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常"}
	}
	item := func(name string) source.Ref { return source.Ref(`{"name":"` + r.List + name + `"}`) }
	return source.Collection{Title: "合集 " + r.List, Items: []source.CollectionItem{
		{Ref: item("1"), Number: 1, Label: r.List + "1"},
		{Ref: item("2"), Number: 2, Label: r.List + "2"},
		{Ref: item("sp"), Unmatched: "集号「SP」不是整数", Label: r.List + "sp"},
	}}, nil
}

func (fakeAdapter) DescribeCollection(ref source.CollectionRef) (source.Display, error) {
	var r fakeListRef
	err := json.Unmarshal(ref, &r)
	return source.Display{URL: "https://fake.test/list/" + r.List, Label: "假合集 " + r.List}, err
}

// seasonBindingServer 在 synctest 气泡里新建连接池，写入 seedCatalog 的目录，在后台运行 SeasonBindingService，
// 起完整的 Echo（目录、绑定、季绑定接口），源适配器只注册了 fakeAdapter。测试结束时先停下后台循环，再关闭连接池。
func seasonBindingServer(t *testing.T, cfg *pgxpool.Config) (*Server, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Open(t, cfg)
	seedCatalog(t, pool)

	logger := slog.New(slog.DiscardHandler)
	env := testenv.New(pool, logger, fakeAdapter{})
	testenv.RunInBackground(t, env.SeasonBindings)

	return New(config.Server{}, config.Dandanplay{}, logger, &Handlers{
		Catalog:       catalog.NewHandler(env.Catalog),
		Binding:       binding.NewHandler(env.Bindings, uploadLimits),
		SeasonBinding: seasonbinding.NewHandler(env.SeasonBindings, uploadLimits),
	}), pool
}

// TestSeasonBindingAPI 合集的季绑定：创建、详情、开关追更、立即补建、删除，以及剧详情、剧列表里的季绑定。
// 种子目录的第 1 季（季 1）有第 1 集（集 2，没有绑定）和第 2 集（集 1，已有两个绑定）。
func TestSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		defaultPatterns := jsonString(source.DefaultEpisodeRule().Patterns())

		// 默认的集号规则
		_, _, data := call(t, srv, http.MethodGet, "/api/episode-rules/default", "", http.StatusOK)
		testenv.AssertJSON(t, data, `{"episodePatterns": `+defaultPatterns+`}`)

		// 创建：201 和详情，只有合集的字段，不含原始 ref；补建在后台进行
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/collection",
			`{"link": " fakelist/s ", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": `+defaultPatterns+`}`, http.StatusCreated)
		wantFields := []string{
			"adapter", "bindingCount", "createdAt", "emptySlots", "episodePatterns", "follow", "id", "items", "kind", "lastCheckedAt", "lastError",
			"link", "mappingFrom", "mappingTo", "numberedByRule", "running", "seasonId", "sourceLabel", "sourceUrl", "status", "title",
		}
		if got := slices.Sorted(maps.Keys(decodeObject(t, data))); !slices.Equal(got, wantFields) {
			t.Errorf("季绑定的字段 = %q\nwant %q", got, wantFields)
		}
		synctest.Wait()

		_, _, data = call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusOK)
		detail := decodeObject(t, data)
		popTime(t, detail, "lastCheckedAt") // 这一轮的开始时间
		popTime(t, detail, "createdAt")
		testenv.AssertJSON(t, json.RawMessage(jsonString(detail)), `{
			"id": 1, "seasonId": 1, "kind": "collection", "adapter": "fake", "link": "fakelist/s",
			"sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"title": "合集 s", "mappingFrom": 1, "mappingTo": 1, "numberedByRule": false, "episodePatterns": `+defaultPatterns+`,
			"follow": true, "status": "active", "lastError": null, "running": false, "bindingCount": 2,
			"items": [
				{"label": "s1", "number": 1, "episodeNumber": 1, "status": "keep", "reason": null, "replaces": null, "lastError": null, "lastErrorAt": null},
				{"label": "s2", "number": 2, "episodeNumber": 2, "status": "keep", "reason": null, "replaces": null, "lastError": null, "lastErrorAt": null},
				{"label": "ssp", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "平台没有给出整数集号", "replaces": null,
				 "lastError": null, "lastErrorAt": null}
			],
			"emptySlots": []
		}`)

		// 剧详情：季带着季绑定（不含条目表），建出的绑定带着季绑定 ID；剧列表带追更标记
		_, _, data = call(t, srv, http.MethodGet, "/api/series/1", "", http.StatusOK)
		var series struct {
			Seasons []struct {
				Number         int               `json:"number"`
				SeasonBindings []json.RawMessage `json:"seasonBindings"`
				Episodes       []struct {
					Bindings []struct {
						SeasonBindingID *int64 `json:"seasonBindingId"`
					} `json:"bindings"`
				} `json:"episodes"`
			} `json:"seasons"`
		}
		if err := json.Unmarshal(data, &series); err != nil {
			t.Fatal(err)
		}
		season := series.Seasons[1]
		if season.Number != 1 || len(season.SeasonBindings) != 1 {
			t.Fatalf("第 1 季的季绑定 = %s", season.SeasonBindings)
		}
		if _, ok := decodeObject(t, season.SeasonBindings[0])["items"]; ok {
			t.Error("剧详情里的季绑定不应带条目表")
		}
		var sources []string
		for _, e := range season.Episodes {
			for _, b := range e.Bindings {
				sources = append(sources, jsonString(b.SeasonBindingID))
			}
		}
		if want := []string{"1", "null", "null", "1"}; !slices.Equal(sources, want) {
			t.Errorf("第 1 季各绑定的 seasonBindingId = %q, want %q", sources, want)
		}
		following := func() string {
			t.Helper()
			_, _, data := call(t, srv, http.MethodGet, "/api/series", "", http.StatusOK)
			var list []struct {
				Following bool `json:"following"`
			}
			if err := json.Unmarshal(data, &list); err != nil {
				t.Fatal(err)
			}
			return jsonString(list)
		}
		if got := following(); got != `[{"following":true},{"following":false},{"following":false}]` {
			t.Errorf("剧列表的追更标记 = %s", got)
		}

		// 关掉追更：返回详情
		_, _, data = call(t, srv, http.MethodPatch, "/api/season-bindings/1", `{"follow": false}`, http.StatusOK)
		if got := decodeObject(t, data); string(got["follow"]) != "false" || string(got["mappingFrom"]) != "1" {
			t.Errorf("PATCH 之后 follow = %s, mappingFrom = %s", got["follow"], got["mappingFrom"])
		}
		if got := following(); got != `[{"following":false},{"following":false},{"following":false}]` {
			t.Errorf("关掉追更后的追更标记 = %s", got)
		}

		// 立即补建：202 和详情（补建刚开始）；正在补建时 409
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusAccepted)
		if got := decodeObject(t, data); string(got["running"]) != "true" || string(got["id"]) != "1" {
			t.Errorf("立即补建返回 running = %s、id = %s", got["running"], got["id"])
		}
		synctest.Wait()
		lease, ok, err := database.TryLease(t.Context(), pool, slog.New(slog.DiscardHandler), database.LeaseSeasonBackfill(1))
		if err != nil || !ok {
			t.Fatalf("TryLease = %v, %v", ok, err)
		}
		assertAPIErrors(t, srv, []apiError{{http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusConflict, "正在补建"}})
		lease.Release()

		// 删除：它建出的绑定一起删掉，见 service 的 TestDeleteSeasonBinding
		if code, _, data := call(t, srv, http.MethodDelete, "/api/season-bindings/1", "", http.StatusOK); code != 0 || string(data) != "null" {
			t.Errorf("DELETE: code=%d data=%s, want 0 null", code, data)
		}
		call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusNotFound)
	})
}

// sha256s 文件内容的 SHA-256（十六进制）的 JSON 数组，文件夹的预览里代替文件本身。
func sha256s(files ...uploadFile) string {
	sums := make([]string, len(files))
	for i, f := range files {
		sum := sha256.Sum256(f.data)
		sums[i] = hex.EncodeToString(sum[:])
	}
	return jsonString(sums)
}

// fileBindingCounts 从剧详情里列出各集用弹幕文件建的绑定的弹幕条数（键为集 ID），没有这种绑定的集不列出。
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

// folderRules 文件夹的表单里的集号规则（默认规则）与集号对应（第 1 集为本地第 to 集），不带花括号。
func folderRules(to int) string {
	return `"episodePatterns": ` + jsonString(source.DefaultEpisodeRule().Patterns()) + `, "mappingFrom": 1, "mappingTo": ` + jsonString(to)
}

// TestFolderSeasonBindingAPI 文件夹的季绑定：预览新建、创建（multipart）、详情、预览编辑（换或不换文件夹）、保存（带或不带文件）、
// 立即补建（200）、删除。只有文件夹的字段，条目名称是"季绑定标题 / 条目名称"；没有追更。各种写入的语义见 seasonbinding 的 folder_test。
// 种子目录的第 1 季（季 1）有第 1 集（集 2）和第 2 集（集 1，已有两个手动的绑定）。
func TestFolderSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg)
		files := []uploadFile{
			danmakuXML("来自新世界/01.xml", "1", "2"),
			danmakuXML("来自新世界/02/a.xml", "3"),
			danmakuXML("来自新世界/02/b.XML", "3", "4"),
			danmakuXML("来自新世界/花絮.xml", "5"),
		}
		entries := `[{"label": "01", "sha256s": ` + sha256s(files[0]) + `}, {"label": "02", "sha256s": ` + sha256s(files[1], files[2]) + `},
			{"label": "花絮", "sha256s": ` + sha256s(files[3]) + `}]`

		// 预览新建：只传文件夹名、条目名称和文件哈希
		_, _, data := call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/folder/preview",
			`{"folder": "来自新世界", "entries": `+entries+`, `+folderRules(1)+`}`, http.StatusOK)
		testenv.AssertJSON(t, data, `{"candidates": [{
			"collectionKind": null, "title": "来自新世界", "sourceUrl": null, "sourceLabel": null, "finished": false,
			"numberedByRule": true, "current": false,
			"items": [
				{"label": "来自新世界 / 01", "number": 1, "episodeNumber": 1, "status": "create", "reason": null, "replaces": null},
				{"label": "来自新世界 / 02", "number": 2, "episodeNumber": 2, "status": "create", "reason": null, "replaces": null},
				{"label": "来自新世界 / 花絮", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "不符合集号规则", "replaces": null}
			],
			"removed": [], "summary": {"create": 2, "replace": 0, "remove": 0, "keep": 0}
		}]}`)

		// 创建：201 和详情，只有文件夹的字段；当场建出了绑定
		_, data = sendMultipart(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/folder", files,
			map[string]string{"form": `{` + folderRules(1) + `}`}, http.StatusCreated)
		detail := decodeObject(t, data)
		wantFields := []string{
			"bindingCount", "createdAt", "emptySlots", "entryCount", "episodePatterns", "id", "items", "kind",
			"mappingFrom", "mappingTo", "seasonId", "title",
		}
		if got := slices.Sorted(maps.Keys(detail)); !slices.Equal(got, wantFields) {
			t.Errorf("季绑定的字段 = %q\nwant %q", got, wantFields)
		}
		popTime(t, detail, "createdAt")
		testenv.AssertJSON(t, json.RawMessage(jsonString(detail)), `{
			"id": 1, "seasonId": 1, "kind": "folder", "title": "来自新世界",
			"episodePatterns": `+jsonString(source.DefaultEpisodeRule().Patterns())+`, "mappingFrom": 1, "mappingTo": 1,
			"bindingCount": 2, "entryCount": 3,
			"items": [
				{"label": "来自新世界 / 01", "number": 1, "episodeNumber": 1, "status": "keep", "reason": null, "replaces": null,
				 "lastError": null, "lastErrorAt": null},
				{"label": "来自新世界 / 02", "number": 2, "episodeNumber": 2, "status": "keep", "reason": null, "replaces": null,
				 "lastError": null, "lastErrorAt": null},
				{"label": "来自新世界 / 花絮", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "不符合集号规则",
				 "replaces": null, "lastError": null, "lastErrorAt": null}
			],
			"emptySlots": []
		}`)
		// 条目 02 的两份文件合成一个绑定（弹幕 3、4），到了第 2 集（集 1）；01 到了第 1 集（集 2）
		if got, want := fileBindingCounts(t, srv, 1), map[int64][]int32{1: {2}, 2: {2}}; !maps.EqualFunc(got, want, slices.Equal) {
			t.Errorf("各集用弹幕文件建的绑定的弹幕条数 = %v, want %v", got, want)
		}

		// 预览编辑，不换文件夹：按保存的条目算，current 为 true，removed 是保存后删除的槽位
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/folder/preview", `{`+folderRules(2)+`}`, http.StatusOK)
		var preview struct {
			Candidates []struct {
				Title   string          `json:"title"`
				Current bool            `json:"current"`
				Removed json.RawMessage `json:"removed"`
				Summary json.RawMessage `json:"summary"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(data, &preview); err != nil {
			t.Fatal(err)
		}
		if c := preview.Candidates[0]; c.Title != "来自新世界" || !c.Current {
			t.Errorf("预览编辑 = %+v", c)
		}
		testenv.AssertJSON(t, preview.Candidates[0].Summary, `{"create": 0, "replace": 1, "remove": 1, "keep": 0}`)

		// 预览编辑，换文件夹：条目按名称和文件哈希对上
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/folder/preview",
			`{"folder": "来自新世界 BD", "entries": `+entries+`, `+folderRules(1)+`}`, http.StatusOK)
		if err := json.Unmarshal(data, &preview); err != nil {
			t.Fatal(err)
		}
		testenv.AssertJSON(t, preview.Candidates[0].Summary, `{"create": 0, "replace": 0, "remove": 0, "keep": 2}`)

		// 保存，不带文件：只改集号对应，返回 200 和详情
		_, data = sendMultipart(t, srv, http.MethodPut, "/api/season-bindings/1/folder", nil,
			map[string]string{"form": `{` + folderRules(2) + `}`}, http.StatusOK)
		if got := decodeObject(t, data); string(got["mappingTo"]) != "2" || string(got["bindingCount"]) != "1" {
			t.Errorf("保存后 mappingTo = %s、bindingCount = %s", got["mappingTo"], got["bindingCount"])
		}

		// 保存，带文件：换文件夹，标题改为新的文件夹名
		bd := []uploadFile{danmakuXML("来自新世界 BD/01.xml", "1", "2"), danmakuXML("来自新世界 BD/02.xml", "6")}
		_, data = sendMultipart(t, srv, http.MethodPut, "/api/season-bindings/1/folder", bd,
			map[string]string{"form": `{` + folderRules(1) + `}`}, http.StatusOK)
		if got := decodeObject(t, data); string(got["title"]) != `"来自新世界 BD"` || string(got["entryCount"]) != "2" ||
			string(got["bindingCount"]) != "2" {
			t.Errorf("换文件夹后 title = %s、entryCount = %s、bindingCount = %s", got["title"], got["entryCount"], got["bindingCount"])
		}

		// 立即补建：当场完成，200 和详情
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusOK)
		if got := decodeObject(t, data); string(got["kind"]) != `"folder"` || string(got["bindingCount"]) != "2" {
			t.Errorf("立即补建返回 kind = %s、bindingCount = %s", got["kind"], got["bindingCount"])
		}

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPatch, "/api/season-bindings/1", `{"follow": true}`, http.StatusBadRequest, "这不是合集的季绑定"},
		})
		call(t, srv, http.MethodDelete, "/api/season-bindings/1", "", http.StatusOK)
		if got := fileBindingCounts(t, srv, 1); len(got) != 0 {
			t.Errorf("删除季绑定后还有用弹幕文件建的绑定：%v", got)
		}
	})
}

// TestFolderSeasonBindingAPIErrors 文件夹的季绑定的接口：上限、文件夹的结构、表单不对时 400，文件解析失败时 422，
// 什么都不保存；对合集的季绑定调用 /folder 为 400。
func TestFolderSeasonBindingAPIErrors(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		const create = "/api/seasons/1/season-bindings/folder"
		form := map[string]string{"form": `{` + folderRules(1) + `}`}
		one := []uploadFile{danmakuXML("星海旅人/1.xml", "1")}

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
			fields      map[string]string
			wantStatus  int
			wantMessage string
		}{
			{"没有文件", create, nil, form, http.StatusBadRequest, "请选择要上传的文件"},
			{"超过 60 份", create, many, form, http.StatusBadRequest, "一次最多上传 60 份文件"},
			{"单份超过 10 MB", create, []uploadFile{big}, form, http.StatusBadRequest, "「星海旅人/1/大.xml」超过 10 MB"},
			{"合计超过 60 MB", create, nineMB, form, http.StatusBadRequest, "一次上传的文件合计不能超过 60 MB"},
			{
				"超过两层", create,
				[]uploadFile{danmakuXML("星海旅人/1/a/1.xml", "1")},
				form,
				http.StatusBadRequest, "目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：星海旅人/1/a/1.xml",
			},
			{"没有文件夹", create, []uploadFile{danmakuXML("1.xml", "1")}, form, http.StatusBadRequest, "文件路径不合法：1.xml"},
			{"不是 .xml", create, []uploadFile{danmakuXML("星海旅人/1/1.ass", "1")}, form, http.StatusBadRequest, "只能上传 XML 弹幕文件：星海旅人/1/1.ass"},
			{
				"文件夹名不一致", create,
				[]uploadFile{danmakuXML("星海旅人/1/1.xml", "1"), danmakuXML("别的/2.xml", "1")},
				form,
				http.StatusBadRequest, "文件要在同一个文件夹里：别的/2.xml",
			},
			{
				"条目名称重复", create,
				[]uploadFile{danmakuXML("星海旅人/1/a.xml", "1"), danmakuXML("星海旅人/1.xml", "1")},
				form,
				http.StatusBadRequest, "条目名称重复：1",
			},
			{"没有表单", create, one, nil, http.StatusBadRequest, "请求参数错误"},
			{"集号对应不对", create, one, map[string]string{"form": `{"mappingFrom": 1}`}, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{"季 ID 不合法", "/api/seasons/0/season-bindings/folder", one, form, http.StatusBadRequest, "季 ID 不合法"},
			{"季不存在", "/api/seasons/99/season-bindings/folder", one, form, http.StatusNotFound, "季不存在"},
			{
				"解析失败", create,
				[]uploadFile{danmakuXML("星海旅人/1.xml", "1"), {name: "星海旅人/2/坏.xml", data: []byte("<i><d")}},
				form,
				http.StatusUnprocessableEntity, "无法识别「星海旅人/2/坏.xml」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
			},
		} {
			if message, _ := sendMultipart(t, srv, http.MethodPost, tt.target, tt.files, tt.fields, tt.wantStatus); message != tt.wantMessage {
				t.Errorf("%s：message = %q, want %q", tt.name, message, tt.wantMessage)
			}
		}
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("保存了 %d 个季绑定", n)
		}

		collection := testenv.InsertCollectionSeasonBinding(t, pool, 1, "s", 1, 1)
		if message, _ := sendMultipart(t, srv, http.MethodPut, "/api/season-bindings/"+jsonString(collection)+"/folder", nil, form,
			http.StatusBadRequest); message != "这不是文件夹的季绑定" {
			t.Errorf("保存合集的季绑定的文件夹表单：message = %q", message)
		}
		if message, _ := sendMultipart(t, srv, http.MethodPut, "/api/season-bindings/99/folder", nil, form, http.StatusNotFound); message != "季绑定不存在" {
			t.Errorf("保存不存在的季绑定：message = %q", message)
		}

		const preview = "/api/seasons/1/season-bindings/folder/preview"
		hash := sha256s(one[0])
		assertAPIErrors(t, srv, []apiError{
			{http.MethodPost, preview, `{` + folderRules(1) + `}`, http.StatusBadRequest, "请选择弹幕文件夹"},
			{http.MethodPost, preview, `{"folder": "星海旅人", "entries": [], ` + folderRules(1) + `}`, http.StatusBadRequest, "请选择弹幕文件夹"},
			{http.MethodPost, preview, `{"folder": " ", "entries": [{"label": "1", "sha256s": ` + hash + `}], ` + folderRules(1) + `}`, http.StatusBadRequest, "请选择弹幕文件夹"},
			{
				http.MethodPost, preview, `{"folder": "星海旅人", "entries": [{"label": "1", "sha256s": ` + hash + `}, {"label": "1", "sha256s": ` + hash + `}], ` + folderRules(1) + `}`,
				http.StatusBadRequest, "条目名称重复：1",
			},
			{http.MethodPost, preview, `{"folder": "星海旅人", "entries": [{"label": "", "sha256s": ` + hash + `}], ` + folderRules(1) + `}`, http.StatusBadRequest, "条目名称不能为空"},
			{http.MethodPost, preview, `{"folder": "星海旅人", "entries": [{"label": "1", "sha256s": []}], ` + folderRules(1) + `}`, http.StatusBadRequest, "条目「1」没有文件"},
			{http.MethodPost, preview, `{"folder": "星海旅人", "entries": [{"label": "1", "sha256s": ["xyz"]}], ` + folderRules(1) + `}`, http.StatusBadRequest, "文件哈希不合法：xyz"},
			{http.MethodPost, preview, `{"folder": "星海旅人", "entries": [{"label": "1", "sha256s": ` + hash + `}]}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/99/season-bindings/folder/preview", `{"folder": "星海旅人", "entries": [{"label": "1", "sha256s": ` + hash + `}], ` + folderRules(1) + `}`, http.StatusNotFound, "季不存在"},

			{http.MethodPost, "/api/season-bindings/99/folder/preview", `{` + folderRules(1) + `}`, http.StatusNotFound, "季绑定不存在"},
			{http.MethodPost, "/api/season-bindings/" + jsonString(collection) + "/folder/preview", `{` + folderRules(1) + `}`, http.StatusBadRequest, "这不是文件夹的季绑定"},
			{http.MethodPost, "/api/season-bindings/" + jsonString(collection) + "/folder/preview", `{"folder": "星海旅人", ` + folderRules(1) + `}`, http.StatusBadRequest, "文件夹名和条目要同时给出"},
		})
	})
}

func TestSeasonBindingAPIErrors(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg)
		const create = "/api/seasons/1/season-bindings/collection"
		call(t, srv, http.MethodPost, create, `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusCreated)
		synctest.Wait()

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPost, "/api/seasons/0/season-bindings/collection", `{"link": "fakelist/s"}`, http.StatusBadRequest, "季 ID 不合法"},
			{http.MethodPost, create, `{"link": "  "}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPost, create, `{"link": `, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingTo": 1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1.5, "mappingTo": 1}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": 1}`, http.StatusBadRequest, "至少要有一条集号规则"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(("]}`, http.StatusBadRequest, "第 1 条集号规则不是合法的正则：missing closing )"},
			{http.MethodPost, create, `{"link": "fakelist/t", "collectionKind": "pages", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "链接里没有这种合集，请重新预览"},
			{http.MethodPost, create, `{"link": "https://example.com/v/1", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "无法识别的链接"},
			{http.MethodPost, create, `{"link": "fakelist/gone", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusUnprocessableEntity, "合集不存在或已删除"},
			{http.MethodPost, create, `{"link": "fakelist/down", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadGateway, "B 站接口异常"},
			{http.MethodPost, create, `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusConflict, "这一季已经绑定过这个合集"},
			{http.MethodPost, "/api/seasons/99/season-bindings/collection", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusNotFound, "季不存在"},

			{http.MethodGet, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodGet, "/api/season-bindings/abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodGet, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodPatch, "/api/season-bindings/1", `{}`, http.StatusBadRequest, "没有要修改的内容"},
			{http.MethodPatch, "/api/season-bindings/1", `{"follow": "yes"}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPatch, "/api/season-bindings/99", `{"follow": true}`, http.StatusNotFound, "季绑定不存在"},

			{http.MethodPost, "/api/season-bindings/0/backfill", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodPost, "/api/season-bindings/99/backfill", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodDelete, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodDelete, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},
		})
	})
}

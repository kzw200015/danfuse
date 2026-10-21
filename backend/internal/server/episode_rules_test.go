package server

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/handler"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Defaults, templates and invalid configs must work without a service, DB or platform.
func TestEpisodeRuleEndpoints(t *testing.T) {
	t.Parallel()
	srv := New(config.Server{}, slog.New(slog.DiscardHandler), complete(&Handlers{
		SeasonBinding: handler.NewHandler(nil, uploadLimits),
	}))
	_, _, data := call(t, srv, http.MethodPost, "/api/episode-rules/template",
		`{"templateId":"seasonNumber","season":2,"adjustment":12}`, http.StatusOK)
	pattern, err := source.RenderEpisodeTemplate("seasonNumber", new(2), 12)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertJSON(t, data, `{"rule":`+jsonString(pattern)+`,"example":"第二季01"}`)
	assertAPIErrors(t, srv, []apiError{
		{http.MethodPost, "/api/episode-rules/template", `{"templateId":"unknown"}`, 400, "未知的集号模板"},
		{http.MethodPost, "/api/episode-rules/template", `{"templateId":"ep","season":2}`, 400, "此模板不支持指定来源季"},
		{http.MethodPost, "/api/episode-rules/template", `{"templateId":"sxe","season":0}`, 400, "来源季必须为 1～9999 的整数"},
		{http.MethodPost, "/api/episode-rules/template", `{"templateId":"sxe","adjustment":2147483648}`, 400, "集号加减值越界"},
	})
	const endpoint = "/api/seasons/1/season-bindings/collection/preview"
	for _, tc := range []struct {
		fields, message string
	}{
		{`"episodeRule":null,"episodePatterns":[]`, "episodeRule 和 episodePatterns 不能同时提供"},
		{`"episodeRule":{},"episodePatterns":null`, "episodeRule 和 episodePatterns 不能同时提供"},
		{`"episodeRule":{"version":3,"scope":"entry"}`, "集号规则版本不合法"},
		{`"episodeRule":{"version":2,"scope":"unknown"}`, "匹配范围必须是 full 或 entry"},
		{`"episodeRule":{"version":2,"scope":"entry","rules":[]}`, "至少要有一条集号规则"},
		{`"episodeRule":{"version":2,"scope":"entry","rules":[{"pattern":"abc"}]}`, `第 1 条集号规则里要有一个捕获组，例如 第(\d+)集`},
		{`"episodeRule":{"version":2,"scope":"entry","rules":[{"pattern":"(1)","adjustment":2147483648}]}`, "第 1 条规则的集号加减值越界"},
		{`"episodeRule":{"version":2,"scope":"entry","rules":[{"pattern":"(1)"}],"excludes":[{"pattern":"sp","kind":"bad"}]}`, "第 1 条排除规则种类不合法"},
		{`"episodeRule":{"version":2,"scope":"entry","rules":[{"pattern":"(` + strings.Repeat("a", 199) + `)"}]}`, "第 1 条集号规则不能超过 200 个字符"},
		{`"episodeRule":{"version":2,"scope":"entry","rules":` + jsonString(make([]source.EpisodePattern, 11)) + `}`, "集号规则最多 10 条"},
		{`"episodeRule":{"version":1,"scope":"entry","rules":[{"pattern":"(1)"}]}`, "旧版规则只支持完整标题且不能排除条目"},
	} {
		assertAPIErrors(t, srv, []apiError{{
			http.MethodPost, endpoint, `{"link":"fakelist/s","mappingFrom":1,"mappingTo":1,` + tc.fields + `}`, 400, tc.message,
		}})
	}
}

func TestEpisodeRuleAPIWriteProtection(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg, fakeAdapter{})
		modern := `"episodeRule":` + jsonString(source.DefaultEpisodeRuleV2().Config()) + `,"mappingFrom":1,"mappingTo":1`
		legacy := `"episodePatterns":` + jsonString(source.DefaultEpisodeRule().Patterns()) + `,"mappingFrom":1,"mappingTo":1`
		_, _, data := call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/collection",
			`{"link":"fakelist/s",`+modern+`}`, http.StatusCreated)
		if !strings.Contains(string(data), `"version":2`) {
			t.Fatalf("missing config: %s", data)
		}
		synctest.Wait()
		assertAPIErrors(t, srv, []apiError{{
			http.MethodPut, "/api/season-bindings/1/collection", `{"link":"fakelist/s",` + legacy + `}`, 409,
			"此绑定已使用新版集号规则，请刷新客户端后重试",
		}})
		_, data = sendMultipart(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/folder",
			[]uploadFile{danmakuXML("某番/01.xml", "1")},
			map[string]string{"form": `{` + modern + `}`}, http.StatusCreated)
		id := decodeObject(t, data)["id"]
		message, _ := sendMultipart(t, srv, http.MethodPut, "/api/season-bindings/"+string(id)+"/folder", nil,
			map[string]string{"form": `{` + legacy + `}`}, http.StatusConflict)
		if !strings.Contains(message, "请刷新客户端") {
			t.Fatalf("folder downgrade response: %s", message)
		}
	})
}

package server

import (
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestSeasonUploadPreview 按季上传的预览：按集号规则从条目名称认出序号，与季绑定预览按规则编号的合集同一套规则。
func TestSeasonUploadPreview(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)
	const target = "/api/seasons/1/file-bindings/preview"

	// 默认规则认出"文件夹 / 子目录名"结尾的集号，全角数字按 NFKC 清洗后也能认出
	_, _, data := call(t, srv, http.MethodPost, target,
		`{"labels": ["来自新世界 / 1", "来自新世界 / ２", "来自新世界 / 特典"], "episodePatterns": `+jsonString(source.DefaultEpisodeRule().Patterns())+`}`,
		http.StatusOK)
	testenv.AssertJSON(t, data, `{"items": [
		{"label": "来自新世界 / 1", "number": 1, "reason": null},
		{"label": "来自新世界 / ２", "number": 2, "reason": null},
		{"label": "来自新世界 / 特典", "number": null, "reason": "不符合集号规则"}
	]}`)

	// 自定义规则（每条去掉前后的空白）；序号相同的条目都标为集号重复，名称相同的条目各自保留
	_, _, data = call(t, srv, http.MethodPost, target,
		`{"labels": ["Legal High / 1-「a」", "Legal High / 2-「b」", "Legal High / 02-「c」", "Legal High / 2-「b」", "Legal High / x-「d」"],
		  "episodePatterns": [" / (\\w+)-「 "]}`,
		http.StatusOK)
	testenv.AssertJSON(t, data, `{"items": [
		{"label": "Legal High / 1-「a」", "number": 1, "reason": null},
		{"label": "Legal High / 2-「b」", "number": null, "reason": "集号重复"},
		{"label": "Legal High / 02-「c」", "number": null, "reason": "集号重复"},
		{"label": "Legal High / 2-「b」", "number": null, "reason": "集号重复"},
		{"label": "Legal High / x-「d」", "number": null, "reason": "集号「x」不是整数"}
	]}`)
}

func TestSeasonUploadPreviewErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	assertAPIErrors(t, srv, []apiError{
		{http.MethodPost, "/api/seasons/0/file-bindings/preview", `{"labels": ["a / 1"], "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "季 ID 不合法"},
		{http.MethodPost, "/api/seasons/1/file-bindings/preview", `{"labels": [], "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "请选择弹幕文件夹"},
		{http.MethodPost, "/api/seasons/1/file-bindings/preview", `{"episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "请选择弹幕文件夹"},
		{http.MethodPost, "/api/seasons/1/file-bindings/preview", `{"labels": ["a / 1"], "episodePatterns": ["(\\d+)", "第\\d+集"]}`, http.StatusBadRequest, `第 2 条集号规则里要有一个捕获组，例如 第(\d+)集`},
		{http.MethodPost, "/api/seasons/1/file-bindings/preview", `{"labels": ["a / 1"]}`, http.StatusBadRequest, "至少要有一条集号规则"},
		{http.MethodPost, "/api/seasons/1/file-bindings/preview", `{"labels": `, http.StatusBadRequest, "请求参数错误"},
		{http.MethodPost, "/api/seasons/99/file-bindings/preview", `{"labels": ["a / 1"], "episodePatterns": ["(\\d+)"]}`, http.StatusNotFound, "季不存在"},
	})
}

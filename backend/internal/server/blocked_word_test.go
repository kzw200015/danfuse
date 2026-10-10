package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/blockword"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestBlockedWords 屏蔽词的增删查：响应结构、状态码与参数校验。判断重复、生效的规则见 blockword 与 danmaku 包的测试。
func TestBlockedWords(t *testing.T) {
	t.Parallel()
	srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.DiscardHandler), &Handlers{
		BlockedWord: blockword.NewHandler(blockword.NewService(dbtest.Pool(t))),
	})

	_, _, data := call(t, srv, http.MethodGet, "/api/blocked-words", "", http.StatusOK)
	testenv.AssertJSON(t, data, `[]`)

	_, _, data = call(t, srv, http.MethodPost, "/api/blocked-words", `{"kind": "keyword", "pattern": " 剧透 "}`, http.StatusCreated)
	obj := decodeObject(t, data)
	popTime(t, obj, "createdAt")
	testenv.AssertJSON(t, obj, `{"id": 1, "kind": "keyword", "pattern": "剧透"}`)
	call(t, srv, http.MethodPost, "/api/blocked-words", `{"kind": "regex", "pattern": "^\\d+$"}`, http.StatusCreated)

	_, _, data = call(t, srv, http.MethodGet, "/api/blocked-words", "", http.StatusOK)
	var words []map[string]json.RawMessage
	if err := json.Unmarshal(data, &words); err != nil {
		t.Fatal(err)
	}
	for _, w := range words {
		popTime(t, w, "createdAt")
	}
	testenv.AssertJSON(t, words, `[
		{"id": 2, "kind": "regex", "pattern": "^\\d+$"},
		{"id": 1, "kind": "keyword", "pattern": "剧透"}
	]`)

	_, _, data = call(t, srv, http.MethodDelete, "/api/blocked-words/1", "", http.StatusOK)
	testenv.AssertJSON(t, data, `null`)

	assertAPIErrors(t, srv, []apiError{
		{http.MethodPost, "/api/blocked-words", `{"kind": "regex", "pattern": "^\\d+$"}`, http.StatusConflict, "已有相同的屏蔽词"},
		{http.MethodPost, "/api/blocked-words", `{"kind": "regex", "pattern": "(前排"}`, http.StatusBadRequest, "不是合法的正则：missing closing )"},
		{http.MethodPost, "/api/blocked-words", `{"kind": "keyword", "pattern": "  "}`, http.StatusBadRequest, "屏蔽词不能为空"},
		{http.MethodPost, "/api/blocked-words", `{"pattern": "前排"}`, http.StatusBadRequest, "屏蔽词的类型只能是关键词或正则"},
		{http.MethodPost, "/api/blocked-words", `{"kind": `, http.StatusBadRequest, "请求参数错误"},
		{http.MethodDelete, "/api/blocked-words/1", "", http.StatusNotFound, "屏蔽词不存在"},
		{http.MethodDelete, "/api/blocked-words/0", "", http.StatusBadRequest, "屏蔽词 ID 不合法"},
	})
}

package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

// uploadFile multipart 请求里的一份文件。
type uploadFile struct {
	name string
	data []byte
}

// danmakuXML 一份 B 站 XML 弹幕文件，每条弹幕的 dmid 依次为 ids。
func danmakuXML(name string, ids ...string) uploadFile {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><i><chatid>934042</chatid>`)
	for _, id := range ids {
		b.WriteString(`<d p="1.5,1,25,16777215,1373250214,0,d9df08a7,` + id + `">弹幕 ` + id + `</d>`)
	}
	b.WriteString("</i>")
	return uploadFile{name: name, data: []byte(b.String())}
}

// upload 以 multipart 发出文件（字段 files），检查状态码，返回解出的统一响应。
func upload(t *testing.T, srv *Server, target string, files []uploadFile, wantStatus int) (message string, data json.RawMessage) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, f := range files {
		part, err := w.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, &body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	_, message, data = decodeResponse(t, "POST "+target, rec, wantStatus)
	return message, data
}

func TestFileBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	// 创建：201 和绑定，没有适配器、链接和时长
	_, data := upload(t, srv, "/api/episodes/2/file-bindings",
		[]uploadFile{danmakuXML("20130709.xml", "1", "2"), danmakuXML("20130711.xml", "2", "3")}, http.StatusCreated)
	assertJSON(t, data, `{
		"id": 5, "kind": "file", "adapter": null, "sourceUrl": null, "sourceLabel": "弹幕文件 · 2 份",
		"title": "20130709", "duration": null, "offset": 0, "status": "active", "danmakuCount": 3,
		"lastFetchedAt": null, "seasonBindingId": null
	}`)

	// 追加文件：已有的跳过
	_, data = upload(t, srv, "/api/bindings/5/files",
		[]uploadFile{danmakuXML("20130711.xml", "2", "3"), danmakuXML("20150111.xml", "3", "4")}, http.StatusOK)
	var appended struct {
		Binding map[string]json.RawMessage `json:"binding"`
		Files   int                        `json:"files"`
		Skipped int                        `json:"skipped"`
		Added   int                        `json:"added"`
	}
	if err := json.Unmarshal(data, &appended); err != nil {
		t.Fatal(err)
	}
	if appended.Files != 1 || appended.Skipped != 1 || appended.Added != 1 || string(appended.Binding["danmakuCount"]) != "4" {
		t.Errorf("追加文件 = %s", data)
	}

	// 文件列表：不含内容
	_, _, data = call(t, srv, http.MethodGet, "/api/bindings/5/files", "", http.StatusOK)
	var files []map[string]json.RawMessage
	if err := json.Unmarshal(data, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || string(files[0]["name"]) != `"20130709.xml"` || string(files[2]["name"]) != `"20150111.xml"` ||
		len(files[0]) != 3 || files[0]["size"] == nil || files[0]["uploadedAt"] == nil {
		t.Errorf("文件列表 = %s", data)
	}

	// 重新解析
	_, _, data = call(t, srv, http.MethodPost, "/api/bindings/5/reparse", "", http.StatusOK)
	if b := decodeObject(t, data); string(b["danmakuCount"]) != "4" || string(b["kind"]) != `"file"` {
		t.Errorf("重新解析 = %s", data)
	}

	// 用弹幕文件建的绑定不能重新拉取；贴链接建的不能追加文件
	if code, message, _ := call(t, srv, http.MethodPost, "/api/bindings/5/refetch", `{}`, http.StatusBadRequest); code != 1 || message != "用弹幕文件建的绑定不能重新拉取" {
		t.Errorf("重新拉取：code=%d message=%q", code, message)
	}
	if message, _ := upload(t, srv, "/api/bindings/1/files", []uploadFile{danmakuXML("1.xml", "1")}, http.StatusBadRequest); message != "这个绑定不是用弹幕文件建的" {
		t.Errorf("给贴链接建的绑定追加文件：message=%q", message)
	}
}

func TestFileBindingUploadErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	many := make([]uploadFile, 51)
	for i := range many {
		many[i] = danmakuXML("1.xml", "1")
	}
	big := uploadFile{name: "大.xml", data: bytes.Repeat([]byte("a"), 10<<20+1)}
	nineMB := uploadFile{name: "9.xml", data: bytes.Repeat([]byte("a"), 9<<20)}

	for _, tt := range []struct {
		name        string
		target      string
		files       []uploadFile
		wantStatus  int
		wantMessage string
	}{
		{"没有文件", "/api/episodes/2/file-bindings", nil, http.StatusBadRequest, "请选择弹幕文件"},
		{"超过 50 份", "/api/episodes/2/file-bindings", many, http.StatusBadRequest, "一次最多上传 50 份文件"},
		{"单份超过 10 MB", "/api/episodes/2/file-bindings", []uploadFile{big}, http.StatusBadRequest, "「大.xml」超过 10 MB"},
		{
			"合计超过 50 MB", "/api/episodes/2/file-bindings",
			[]uploadFile{nineMB, nineMB, nineMB, nineMB, nineMB, nineMB},
			http.StatusBadRequest, "一次上传的文件合计不能超过 50 MB",
		},
		{
			"认不出", "/api/episodes/2/file-bindings",
			[]uploadFile{danmakuXML("1.xml", "1"), {name: "README.html", data: []byte("<html></html>")}},
			http.StatusUnprocessableEntity, "无法识别「README.html」：目前只支持 B 站的 XML 弹幕文件，且文件要完整",
		},
		{"集不存在", "/api/episodes/99/file-bindings", []uploadFile{danmakuXML("1.xml", "1")}, http.StatusNotFound, "集不存在"},
		{"集 ID 不合法", "/api/episodes/0/file-bindings", []uploadFile{danmakuXML("1.xml", "1")}, http.StatusBadRequest, "集 ID 不合法"},
		{"绑定不存在", "/api/bindings/99/files", []uploadFile{danmakuXML("1.xml", "1")}, http.StatusNotFound, "绑定不存在"},
	} {
		if message, _ := upload(t, srv, tt.target, tt.files, tt.wantStatus); message != tt.wantMessage {
			t.Errorf("%s：message = %q, want %q", tt.name, message, tt.wantMessage)
		}
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bindings WHERE kind = 'file'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("建出了 %d 个用弹幕文件建的绑定", n)
	}
}

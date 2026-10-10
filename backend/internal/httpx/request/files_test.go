package request_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
)

type uploadRequest struct {
	Targets string `form:"targets"`
}

func (r *uploadRequest) Validate() error { return nil }

// TestReadFiles 流式读取不受 Go 解析表单时 1000 个 part 的限制，文件名保留目录，读完之后 Bind 照常拿到其他字段。
func TestReadFiles(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("targets", "[]"); err != nil {
		t.Fatal(err)
	}
	const n = 1200
	for i := range n {
		part, err := w.CreateFormFile("files", fmt.Sprintf("文件夹/%d/%d.xml", i, i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(part, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", &body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	c := echo.New().NewContext(req, httptest.NewRecorder())

	files, err := request.ReadFiles(c, request.FileLimits{MaxFiles: n, MaxFileMB: 1, MaxUploadMB: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != n {
		t.Fatalf("读出 %d 份, want %d", len(files), n)
	}
	if f := files[n-1]; f.Name != fmt.Sprintf("文件夹/%d/%d.xml", n-1, n-1) || string(f.Data) != fmt.Sprint(n-1) {
		t.Errorf("最后一份 = %q %v", f.Name, f.Data)
	}
	got, err := request.Bind[uploadRequest](c)
	if err != nil {
		t.Fatal(err)
	}
	if got.Targets != "[]" {
		t.Errorf("targets = %q, want []", got.Targets)
	}
}

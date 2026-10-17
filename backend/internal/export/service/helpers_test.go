package service_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	"github.com/kzw200015/danfuse/backend/internal/export/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// newService 新库里写入 testenv.SeedEpisodes 的剧（第 1 季，集 1、2），返回导出的 service。
func newService(t *testing.T) (*service.Service, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t)
	testenv.SeedEpisodes(t, pool)
	return testenv.New(pool, testenv.Logger(t)).Export, pool
}

// insertBinding 给集 1 写入一个链接绑定和它的弹幕，见 insertEpisodeBinding。
func insertBinding(t *testing.T, pool *pgxpool.Pool, title string, offset float64, ds ...danmaku.Danmaku) int64 {
	t.Helper()
	return insertEpisodeBinding(t, pool, 1, title, offset, ds...)
}

// insertEpisodeBinding 给集 episodeID 写入一个链接绑定和它的弹幕（danmaku_count、max_time_ms 与弹幕一致），返回绑定 ID。
// ref 每次都不同，同一集里可以有标题相同的绑定。
func insertEpisodeBinding(t *testing.T, pool *pgxpool.Pool, episodeID int64, title string, offset float64, ds ...danmaku.Danmaku) int64 {
	t.Helper()
	var maxMs int32
	for _, d := range ds {
		maxMs = max(maxMs, d.TimeMs)
	}
	var id int64
	err := pool.QueryRow(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", danmaku_count, max_time_ms)
		VALUES ($1, 'fake', jsonb_build_object('name', gen_random_uuid()), $2, 1420, $3, $4, $5)
		RETURNING id`, episodeID, title, offset, len(ds), maxMs).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		_, err := pool.Exec(t.Context(), `INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, d.SourceID, d.TimeMs, int16(d.Mode), int32(d.Color), d.Text)
		if err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// insertLargeBinding 给集 episodeID 写入一个有 n 条弹幕的链接绑定，返回绑定 ID。弹幕的时间为 1..n 毫秒、ID 为 1..n，
// 正文是各不相同的 32 位十六进制串，压缩后仍有 n × 十几字节，导出时超过 zip 的写缓冲。
func insertLargeBinding(t *testing.T, pool *pgxpool.Pool, episodeID int64, title string, n int) int64 {
	t.Helper()
	id := insertEpisodeBinding(t, pool, episodeID, title, 0)
	_, err := pool.Exec(t.Context(), `
		WITH d AS (
			INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text)
			SELECT $1, g, g, 1, 0, md5(g::text) FROM generate_series(1, $2::int) g
		)
		UPDATE bindings SET danmaku_count = $2, max_time_ms = $2 WHERE id = $1`, id, n)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// decodeXML 读回导出的一份 XML：每条弹幕一行"时间 模式 颜色 ID 正文"，按 XML 里的顺序。
func decodeXML(t *testing.T, data []byte) []string {
	t.Helper()
	items, err := bilifmt.Decode(data)
	if err != nil {
		t.Fatalf("导出的 XML 解不开：%v\n%s", err, data)
	}
	got := make([]string, len(items))
	for i, d := range items {
		got[i] = fmt.Sprintf("%d %d %06x %d %s", d.TimeMs, d.Mode, d.Color, d.SourceID, d.Text)
	}
	return got
}

// exportedZip 导出的一个 zip：给出的文件名，zip 里的文件路径（按写入的顺序），每份文件读回的弹幕（decodeXML）。
type exportedZip struct {
	Name    string
	Paths   []string
	Danmaku map[string][]string
}

// exportZip 调用 export（集、季的导出）并读回 zip（readZip）。
// 检查文件名恰好给出一次、内容写在给出文件名之后。
func exportZip(t *testing.T, export func(open service.Open) error) exportedZip {
	t.Helper()
	var names []string
	var buf bytes.Buffer
	err := export(func(name string) io.Writer {
		if buf.Len() > 0 {
			t.Error("给出文件名之前已经写了内容")
		}
		names = append(names, name)
		return &buf
	})
	if err != nil {
		t.Fatalf("导出：%v", err)
	}
	if len(names) != 1 {
		t.Fatalf("文件名给出了 %d 次：%q", len(names), names)
	}
	return readZip(t, names[0], buf.Bytes())
}

// readZip 读回导出的名为 name 的 zip，检查每份文件都用 Deflate 压缩。
func readZip(t *testing.T, name string, data []byte) exportedZip {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("导出的 zip 解不开：%v", err)
	}
	got := exportedZip{Name: name, Paths: []string{}, Danmaku: map[string][]string{}}
	for _, f := range zr.File {
		if f.Method != zip.Deflate {
			t.Errorf("%s：压缩方式 = %d, want Deflate", f.Name, f.Method)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("%s：%v", f.Name, err)
		}
		got.Paths = append(got.Paths, f.Name)
		got.Danmaku[f.Name] = decodeXML(t, data)
	}
	return got
}

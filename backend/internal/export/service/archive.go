package service

import (
	"archive/zip"
	"context"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	"github.com/kzw200015/danfuse/backend/internal/export/dao"
)

// archive 集、季导出的 zip（Deflate），边加文件边写出，不在内存里拼出整个文件。
// 第一次加文件时才调用 open：没有可导出的弹幕时一个字节都不写出，调用方据此返回 404，不输出空的 zip。
// 中途出错时不调用 close，zip 没有中央目录，是不完整的。
type archive struct {
	name string // zip 的文件名，交给 open
	open Open
	zw   *zip.Writer
}

func newArchive(name string, open Open) *archive {
	return &archive{name: name, open: open}
}

// add 加一份 XML，path 是在 zip 里的路径（目录用 / 分隔）。
func (a *archive) add(path string, items []danmaku.Danmaku) error {
	if a.zw == nil {
		a.zw = zip.NewWriter(a.open(a.name))
	}
	w, err := a.zw.Create(path) // Create 用 Deflate 压缩
	if err != nil {
		return fmt.Errorf("create %s in zip: %w", path, err)
	}
	if err := bilifmt.Encode(w, items); err != nil {
		return fmt.Errorf("write %s in zip: %w", path, err)
	}
	return nil
}

// empty 还没加过文件（也就还没调用 open）。
func (a *archive) empty() bool {
	return a.zw == nil
}

// close 写出中央目录，zip 完整。
func (a *archive) close() error {
	if err := a.zw.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	return nil
}

// addEpisode 把一集的绑定加进 a 的目录 dir（"" 为 zip 的根，否则以 / 结尾）：每个绑定一份 <绑定标题>.xml（bindingDanmaku），
// 按创建顺序；跳过没有弹幕的绑定（校正后全部丢弃的也算），同名的依次加 " (2)"、" (3)"。
// 按绑定逐个读取弹幕、逐个写出，内存只占一个绑定的量。
func addEpisode(ctx context.Context, q *dao.Queries, a *archive, dir string, episodeID int64) error {
	bindings, err := q.ListBindingsByEpisode(ctx, episodeID)
	if err != nil {
		return fmt.Errorf("list bindings of episode %d: %w", episodeID, err)
	}
	names := fileNames{}
	for _, b := range bindings {
		items, err := bindingDanmaku(ctx, q, b.ID, b.Offset)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			continue
		}
		if err := a.add(dir+names.add(b.Title), items); err != nil {
			return fmt.Errorf("binding %d: %w", b.ID, err)
		}
	}
	return nil
}

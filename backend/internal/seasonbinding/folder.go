package seasonbinding

import (
	"context"

	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
)

// errFolderBackfillUnsupported 文件夹的立即补建还没有实现（工单 12）。
var errFolderBackfillUnsupported = apierr.ErrBadRequest.WithMessage("文件夹的季绑定暂不支持立即补建")

// backfillFolder 文件夹的立即补建：在一个事务里派生、按条目新建和替换槽位，不删除没有条目的槽位（规格第 4 节"文件夹"）。
func (s *Service) backfillFolder(context.Context, seasonbindingdb.SeasonBinding) error {
	return errFolderBackfillUnsupported
}

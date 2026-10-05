// Package service 业务逻辑层。出错时返回 errcode.Error 表达业务语义，其他错误会被视为服务器内部错误。
// 所有读写数据库的业务都在这一层；catalog 等领域包只有接口、类型、纯计算和外部适配。
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/wire"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var ProviderSet = wire.NewSet(
	NewCatalogService,
	NewBindingService,
	NewSyncService,
	NewSeasonBindingService,
	NewLocalProvider,
)

// errShuttingDown 后台循环（SyncService、SeasonBindingService 的 Run）已经返回、服务正在关闭时，手动触发返回 503。
var errShuttingDown = errcode.ErrServiceUnavailable.WithMessage("服务正在关闭")

// fetchTimeout 一次拉取的总时限，创建绑定时也包括解析链接（跟随短链也要联网），季绑定的预览、创建与补建也用它限定
// 识别链接、列出合集：server.write_timeout 是 30 秒，留出写库和响应的时间。超时由适配器按 Upstream 返回。
const fetchTimeout = 25 * time.Second

// fetch 拉取一个弹幕源的全部弹幕，总时限 fetchTimeout。调用方拉完才开写入事务。
func fetch(ctx context.Context, adapter source.Adapter, ref source.Ref) (source.Fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	return adapter.Fetch(ctx, ref)
}

// sourceError 把适配器的 *source.Error 转成管理 API 的错误：InvalidLink 为 400，NotFound 为 422，其余为 502；
// 提示用适配器写的 Message，底层原因只进日志。其他错误原样返回，按服务器内部错误处理。
func sourceError(err error) error {
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		return err
	}
	base := errcode.ErrBadGateway
	switch srcErr.Kind {
	case source.InvalidLink:
		base = errcode.ErrBadRequest
	case source.NotFound:
		base = errcode.ErrUnprocessable
	}
	return base.WithMessage(srcErr.Message).Wrap(err)
}

// 以下为可空的列与 Go 类型之间的转换，同步写入、本地 Provider 读取与季绑定共用。

// nullIfEmpty 空串存为 null。
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	return new(int32(*v))
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	return new(int(*v))
}

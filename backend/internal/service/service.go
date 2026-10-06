// Package service 业务逻辑层。出错时返回 apierr.Error 表达业务语义，其他错误会被视为服务器内部错误。
// 所有读写数据库的业务都在这一层；catalog 等领域包只有接口、类型、纯计算和外部适配。
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/wire"

	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
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
var errShuttingDown = apierr.ErrServiceUnavailable.WithMessage("服务正在关闭")

// internalErrorMessage 后台任务遇到服务器内部错误时存下来给管理界面看的原因（补建记在季绑定、条目上，同步记在同步记录上），
// 完整的错误进日志。
const internalErrorMessage = "服务器内部错误，详见日志"

// fetchTimeout 一次拉取的总时限，创建绑定时也包括解析链接（跟随短链也要联网），季绑定的预览、创建与补建也用它限定
// 识别链接、列出合集：server.write_timeout 不小于 config.MinWriteTimeout（30 秒），留出写库和响应的时间。改它时一起改那个下限。
// 超时由适配器按 Upstream 返回。
const fetchTimeout = 25 * time.Second

// fetch 拉取一个弹幕源的全部弹幕，总时限 fetchTimeout。调用方拉完才开写入事务。
func fetch(ctx context.Context, adapter source.Adapter, ref source.Ref) (source.Fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	return adapter.Fetch(ctx, ref)
}

// sourceAPIError 把适配器的 *source.Error 转成管理 API 的错误：InvalidLink 为 400，NotFound 为 422，其余为 502；
// 提示用适配器写的 Message，底层原因 Err 只进日志（不再包一层 *source.Error，日志里提示不重复）。
// 其他错误原样返回，按服务器内部错误处理。转换后取不出 Kind，要按 Kind 分支的调用方（追更）用转换之前的错误。
func sourceAPIError(err error) error {
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		return err
	}
	base := apierr.ErrBadGateway
	switch srcErr.Kind {
	case source.InvalidLink:
		base = apierr.ErrBadRequest
	case source.NotFound:
		base = apierr.ErrUnprocessable
	}
	return base.WithMessage(srcErr.Message).Wrap(srcErr.Err)
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

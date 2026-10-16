// Package service 目录的业务层。
//   - service.go：Service 与错误；series.go：剧、季、集的浏览（剧列表、剧详情）、删除与海报；
//   - search.go：季的搜索列；
//   - sync.go：同步的调度（后台循环、手动触发、租约、同步记录）；sync_write.go：按剧把目录源给的数据写进目录。
//
// 目录源适配的接口与交换的类型在根包 catalog；季的名称、名称里的季号集号在 naming 包。
package service

import (
	"github.com/jackc/pgx/v5/pgxpool"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/dao"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	seasonbindingsvc "github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
)

var (
	errSeriesNotFound  = apierr.ErrNotFound.WithMessage("剧不存在")
	errSeasonNotFound  = apierr.ErrNotFound.WithMessage("季不存在")
	errEpisodeNotFound = apierr.ErrNotFound.WithMessage("集不存在")
	errImageNotFound   = apierr.ErrNotFound.WithMessage("图片不存在")
)

// Service 管理界面浏览目录，以及手动删除剧、季、集。
//
// 删除用来清理目录源里已经没有的条目，下级的季、集、绑定和弹幕随外键级联删除。同步进行中也能删除，不加应用层的锁：
// 同步正在写这部剧时，删除等它的事务提交，再连同刚写入的内容一起删掉；目录源里还在的条目，之后的同步（包括正在进行的这次）
// 按身份找不到它，会用新 ID 重新建出来，绑定不会恢复。
type Service struct {
	pool           *pgxpool.Pool
	q              *dao.Queries
	catalogSource  catalog.Source            // 剧详情里跳回目录源的链接；未配置目录源时为 nil
	bindings       *bindingsvc.Service       // 剧详情里各集的绑定
	seasonBindings *seasonbindingsvc.Service // 剧详情里各季的季绑定
}

func NewService(pool *pgxpool.Pool, catalogSource catalog.Source, bindings *bindingsvc.Service, seasonBindings *seasonbindingsvc.Service) *Service {
	return &Service{pool: pool, q: dao.New(pool), catalogSource: catalogSource, bindings: bindings, seasonBindings: seasonBindings}
}

package server

import (
	"github.com/labstack/echo/v5"

	bindinghandler "github.com/kzw200015/danfuse/backend/internal/binding/handler"
	blockwordhandler "github.com/kzw200015/danfuse/backend/internal/blockword/handler"
	cataloghandler "github.com/kzw200015/danfuse/backend/internal/catalog/handler"
	dandanhandler "github.com/kzw200015/danfuse/backend/internal/dandan/handler"
	healthhandler "github.com/kzw200015/danfuse/backend/internal/health/handler"
	seasonbindinghandler "github.com/kzw200015/danfuse/backend/internal/seasonbinding/handler"
	settingshandler "github.com/kzw200015/danfuse/backend/internal/settings/handler"
)

// Handlers 汇总各领域的 handler，由 registerRoutes 依次注册它们的路由。
type Handlers struct {
	Health        *healthhandler.Handler
	Settings      *settingshandler.Handler
	Catalog       *cataloghandler.Handler
	Sync          *cataloghandler.SyncHandler
	Binding       *bindinghandler.Handler
	SeasonBinding *seasonbindinghandler.Handler
	BlockedWord   *blockwordhandler.Handler
	Dandan        *dandanhandler.Handler
}

// registerRoutes 管理 API 挂在 /api 下，各领域在自己 handler 包的 routes.go 里注册路由；
// 弹弹 API 的前缀、CORS、Gzip 与 token 由 dandan 自己处理（见 dandan/handler 的 Register）。
func registerRoutes(e *echo.Echo, h *Handlers) {
	api := e.Group("/api")
	h.Health.Register(api)
	h.Settings.Register(api)
	h.Catalog.Register(api)
	h.Sync.Register(api)
	h.Binding.Register(api)
	h.SeasonBinding.Register(api)
	h.BlockedWord.Register(api)

	h.Dandan.Register(e)
}

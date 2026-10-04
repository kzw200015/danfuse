package server

import (
	"io/fs"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// backendPrefixes 下的路径归后端：不托管前端文件，没命中的路由按原来的方式返回 404，不回退到 index.html。
var backendPrefixes = []string{"/api", "/dandanplay"}

func isBackendPath(c *echo.Context) bool {
	p := c.Request().URL.Path
	for _, prefix := range backendPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// frontend 托管前端，files 的根目录下是 index.html 与 assets/。
// 文件与 SPA 回退交给 Echo 的 Static 中间件：文件存在就直接返回；路由和文件都没命中时返回 index.html，
// 直接刷新 /catalog/...、/sync 这类前端路由也能打开。这里只补上缓存头。
func frontend(files fs.FS) echo.MiddlewareFunc {
	static := middleware.StaticWithConfig(middleware.StaticConfig{
		Filesystem: files,
		HTML5:      true,
		Skipper:    isBackendPath,
	})
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		serve := static(next)
		return func(c *echo.Context) error {
			if !isBackendPath(c) {
				if err := setCacheControl(c, files); err != nil {
					return err
				}
			}
			return serve(c)
		}
	}
}

// setCacheControl 按请求的文件设置缓存头：assets/ 下的文件名带内容哈希，长期缓存加 immutable；
// 其余（index.html 与 SPA 回退）每次都向服务端确认。
// assets/ 下不存在的文件直接 404，不回退到 index.html，免得 index.html 被当作脚本长期缓存。
func setCacheControl(c *echo.Context, files fs.FS) error {
	header := c.Response().Header()
	name := strings.TrimPrefix(c.Request().URL.Path, "/")
	if !strings.HasPrefix(name, "assets/") {
		header.Set(echo.HeaderCacheControl, "no-cache")
		return nil
	}
	if _, err := fs.Stat(files, name); err != nil {
		return echo.ErrNotFound
	}
	header.Set(echo.HeaderCacheControl, "public, max-age=31536000, immutable")
	return nil
}

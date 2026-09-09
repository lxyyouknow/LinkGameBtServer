// Package adminui 内嵌 LinkGame 统计后台静态页面。
package adminui

import (
	_ "embed"
	"net/http"
)

//go:embed admin.html
var adminHTML []byte

func Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 统计后台 HTML 会随服务端版本更新，任何浏览器、共享代理或 CDN 都不得缓存。
		// 多个兼容头同时保留，避免固定 /admin.html 地址恢复旧页面快照。
		writer.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0, s-maxage=0")
		writer.Header().Set("CDN-Cache-Control", "no-store")
		writer.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		// Cloudflare 若配置了“忽略源站 Cache-Control”的 Edge TTL，仍可能缓存动态 HTML。
		// Vary: * 是最后一道兜底，强制所有共享缓存绕过该响应。
		writer.Header().Set("Vary", "*")
		writer.Header().Set("Pragma", "no-cache")
		writer.Header().Set("Expires", "0")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'",
		)
		writer.WriteHeader(http.StatusOK)
		if request.Method == http.MethodGet {
			_, _ = writer.Write(adminHTML)
		}
	})
}

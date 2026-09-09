package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	corsAllowMethods  = "GET, POST, PUT"
	corsAllowHeaders  = "Authorization, Content-Type"
	corsExposeHeaders = "X-Request-ID"
)

var corsAllowedMethods = map[string]struct{}{
	http.MethodGet:  {},
	http.MethodPost: {},
	http.MethodPut:  {},
}

var corsAllowedRequestHeaders = map[string]struct{}{
	"authorization": {},
	"content-type":  {},
}

func withCORS(
	allowedOrigins []string,
	next http.Handler,
) http.Handler {
	allowedOriginSet := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowedOriginSet[strings.ToLower(origin)] = struct{}{}
	}

	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin == "" {
			// 小游戏原生运行时、服务间请求和 curl 通常没有 Origin。
			next.ServeHTTP(writer, request)
			return
		}

		_, explicitlyAllowed := allowedOriginSet[strings.ToLower(origin)]
		if !explicitlyAllowed && !isSameHostOrigin(origin, request.Host) {
			writeError(
				writer,
				http.StatusForbidden,
				"ORIGIN_NOT_ALLOWED",
				"请求来源不在允许列表中",
				requestIDFromContext(request.Context()),
			)
			return
		}

		addVaryHeader(writer.Header(), "Origin")
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set(
			"Access-Control-Expose-Headers",
			corsExposeHeaders,
		)

		requestedMethod := strings.ToUpper(strings.TrimSpace(
			request.Header.Get("Access-Control-Request-Method"),
		))
		isPreflight := request.Method == http.MethodOptions &&
			requestedMethod != ""
		if !isPreflight {
			next.ServeHTTP(writer, request)
			return
		}

		addVaryHeader(
			writer.Header(),
			"Access-Control-Request-Method",
		)
		addVaryHeader(
			writer.Header(),
			"Access-Control-Request-Headers",
		)

		if _, allowed := corsAllowedMethods[requestedMethod]; !allowed {
			writeError(
				writer,
				http.StatusForbidden,
				"CORS_METHOD_NOT_ALLOWED",
				"跨域请求方法不被允许",
				requestIDFromContext(request.Context()),
			)
			return
		}
		if !corsRequestHeadersAllowed(
			request.Header.Get("Access-Control-Request-Headers"),
		) {
			writeError(
				writer,
				http.StatusForbidden,
				"CORS_HEADERS_NOT_ALLOWED",
				"跨域请求头不被允许",
				requestIDFromContext(request.Context()),
			)
			return
		}

		writer.Header().Set(
			"Access-Control-Allow-Methods",
			corsAllowMethods,
		)
		writer.Header().Set(
			"Access-Control-Allow-Headers",
			corsAllowHeaders,
		)
		writer.Header().Set("Access-Control-Max-Age", "600")
		writer.WriteHeader(http.StatusNoContent)
	})
}

// 管理后台由 API 服务自身托管。同源 POST 也会携带 Origin，必须允许当前 Host，
// 否则即使页面和接口在同一域名，GM 登录仍会被跨域白名单误拦截。
func isSameHostOrigin(origin string, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, strings.TrimSpace(requestHost))
}

func corsRequestHeadersAllowed(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	for _, header := range strings.Split(value, ",") {
		normalized := strings.ToLower(strings.TrimSpace(header))
		if _, allowed := corsAllowedRequestHeaders[normalized]; !allowed {
			return false
		}
	}
	return true
}

func addVaryHeader(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

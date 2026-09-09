package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"linkgame-server/internal/analytics"
	"linkgame-server/internal/gm"
)

func handleAnalyticsIngest(service AnalyticsService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if service == nil {
			writeError(w, 503, "ANALYTICS_DISABLED", "统计采集暂未启用", requestID(r))
			return
		}
		var body struct {
			Events []analytics.Event `json:"events"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		p := playerFromContext(r.Context())
		ids, err := service.Ingest(r.Context(), p.ID, body.Events)
		if err != nil {
			if errors.Is(err, analytics.ErrInvalidEvent) {
				writeError(w, 400, "INVALID_ANALYTICS_EVENT", "统计事件不合法", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"acceptedEventIds": ids, "requestId": requestID(r)})
	})
}

func handleStartupDiagnosticsIngest(service AnalyticsService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if service == nil {
			writeError(w, 503, "ANALYTICS_DISABLED", "统计采集暂未启用", requestID(r))
			return
		}
		var body struct {
			Events []analytics.StartupEvent `json:"events"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		ids, err := service.IngestStartup(r.Context(), body.Events)
		if err != nil {
			if errors.Is(err, analytics.ErrInvalidEvent) {
				writeError(w, 400, "INVALID_STARTUP_EVENT", "启动诊断事件不合法", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"acceptedEventIds": ids, "requestId": requestID(r)})
	})
}

func handleTimezone(service AnalyticsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		timezone := "Asia/Shanghai"
		if service != nil {
			timezone = service.Timezone()
		}
		writeJSON(w, 200, map[string]any{"timezone": timezone, "requestId": requestID(r)})
	}
}

func handleGMLogin(service GMService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		var body struct {
			Account  string `json:"account"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		token, expiresAt, err := service.Login(strings.TrimSpace(body.Account), body.Password)
		if err != nil {
			switch {
			case errors.Is(err, gm.ErrDisabled):
				writeError(w, 503, "ANALYTICS_DISABLED", "统计后台暂未启用", requestID(r))
			case errors.Is(err, gm.ErrRateLimited):
				writeError(w, 429, "GM_RATE_LIMITED", "登录失败次数过多，请稍后再试", requestID(r))
			default:
				writeError(w, 401, "GM_UNAUTHORIZED", "账号或密码错误", requestID(r))
			}
			return
		}
		writeJSON(w, 200, map[string]any{"session": token, "expiresAt": expiresAt, "requestId": requestID(r)})
	}
}
func handleGMLogout(service GMService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		token := bearer(r)
		if token != "" {
			service.Logout(token)
		}
		writeJSON(w, 200, map[string]any{"ok": true, "requestId": requestID(r)})
	}
}

func handleGMStats(gmService GMService, service AnalyticsService, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if service == nil {
			writeError(w, 503, "ANALYTICS_DISABLED", "统计后台暂未启用", requestID(r))
			return
		}
		if !gmService.Authenticate(bearer(r)) {
			writeError(w, 401, "GM_UNAUTHORIZED", "后台登录已失效", requestID(r))
			return
		}
		filter, ok := parseFilter(w, r, service.Timezone())
		if !ok {
			return
		}
		var data any
		var err error
		switch kind {
		case "overview":
			data, err = service.Overview(r.Context(), filter)
		case "overview-trend":
			data, err = service.OverviewTrend(r.Context(), filter)
		case "daily":
			data, err = service.Daily(r.Context(), filter)
		case "levels":
			data, err = service.LevelDistribution(r.Context(), filter)
		case "funnel":
			data, err = service.Funnel(r.Context(), filter)
		case "ads":
			data, err = service.AdPerformance(r.Context(), filter)
		case "ad-failures":
			data, err = service.AdFailures(r.Context(), filter)
		case "startup":
			data, err = service.StartupDiagnostics(r.Context(), filter)
		case "themes":
			data, err = service.ThemeUsage(r.Context(), filter)
		case "props":
			data, err = service.PropUsage(r.Context(), filter)
		case "ranking":
			data, err = service.LevelRanking(r.Context(), filter)
		case "ad-ranking":
			data, err = service.AdRanking(r.Context(), filter)
		}
		if err != nil {
			if errors.Is(err, analytics.ErrInvalidFilter) {
				writeError(w, 400, "INVALID_FILTER", "统计筛选条件不合法", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": data, "requestId": requestID(r)})
	}
}

type filterRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	AppID   int    `json:"appId"`
	SDKType *int   `json:"sdkType"`
	Channel int    `json:"channel"`
}

func parseFilter(w http.ResponseWriter, r *http.Request, timezone string) (analytics.Filter, bool) {
	var body filterRequest
	if !decodeJSON(w, r, &body) {
		return analytics.Filter{}, false
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "统计时区配置错误", requestID(r))
		return analytics.Filter{}, false
	}
	from, err1 := time.ParseInLocation("2006-01-02", body.From, location)
	to, err2 := time.ParseInLocation("2006-01-02", body.To, location)
	if err1 != nil || err2 != nil {
		writeError(w, 400, "INVALID_FILTER", "日期格式应为 YYYY-MM-DD", requestID(r))
		return analytics.Filter{}, false
	}
	if body.AppID == 0 {
		body.AppID = 1
	}
	return analytics.Filter{From: from, To: to.Add(24 * time.Hour), AppID: body.AppID, SDKType: body.SDKType, Channel: body.Channel}, true
}
func bearer(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}

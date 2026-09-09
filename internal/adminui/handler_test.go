package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerDisablesBrowserAndSharedProxyCaching(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/admin.html", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码=%d，期望 200", response.Code)
	}
	cacheControl := response.Header().Get("Cache-Control")
	for _, directive := range []string{"no-store", "no-cache", "must-revalidate", "s-maxage=0"} {
		if !strings.Contains(cacheControl, directive) {
			t.Fatalf("Cache-Control=%q，缺少 %q", cacheControl, directive)
		}
	}
	if response.Header().Get("CDN-Cache-Control") != "no-store" ||
		response.Header().Get("Cloudflare-CDN-Cache-Control") != "no-store" ||
		response.Header().Get("Vary") != "*" ||
		response.Header().Get("Pragma") != "no-cache" ||
		response.Header().Get("Expires") != "0" {
		t.Fatalf("后台禁止缓存响应头不完整: %#v", response.Header())
	}
	if !strings.Contains(response.Body.String(), "经营总览") ||
		!strings.Contains(response.Body.String(), "激励排行") ||
		!strings.Contains(response.Body.String(), "$('#from').value=localDate();$('#to').value=localDate()") ||
		!strings.Contains(response.Body.String(), "/api/gm/stats/ad-ranking") ||
		!strings.Contains(response.Body.String(), "/api/gm/stats/ads") ||
		!strings.Contains(response.Body.String(), "/api/gm/stats/ad-failures") ||
		!strings.Contains(response.Body.String(), "广告加载与曝光") ||
		!strings.Contains(response.Body.String(), "加载广告数") ||
		!strings.Contains(response.Body.String(), "进入主线率") ||
		!strings.Contains(response.Body.String(), "mainLevelEnterRate") ||
		!strings.Contains(response.Body.String(), "最后登录达到第 1 天") ||
		!strings.Contains(response.Body.String(), "/api/gm/stats/overview-trend") ||
		!strings.Contains(response.Body.String(), "dateRangePanel") ||
		!strings.Contains(response.Body.String(), "calendarMonths") ||
		!strings.Contains(response.Body.String(), "data-trend") ||
		!strings.Contains(response.Body.String(), "首次进入小时") ||
		!strings.Contains(response.Body.String(), "granularity==='hour'") ||
		!strings.Contains(response.Body.String(), "首次登录") ||
		!strings.Contains(response.Body.String(), "最近登录") ||
		!strings.Contains(response.Body.String(), "dateTime(x.createdAt)") ||
		!strings.Contains(response.Body.String(), "按广告事件发生日期查看所选日期内全部玩家") ||
		!strings.Contains(response.Body.String(), "data-ad-view=\"rewarded\"") ||
		!strings.Contains(response.Body.String(), "总广告预加载") ||
		!strings.Contains(response.Body.String(), "adPlacementChart") ||
		!strings.Contains(response.Body.String(), "placements.map(x=>x.preloads)") ||
		!strings.Contains(response.Body.String(), "rewardedAdPreloadCreates") ||
		!strings.Contains(response.Body.String(), "interstitialAdLoads") {
		t.Fatal("后台没有提供经营与广告统计入口")
	}
	if !strings.Contains(response.Body.String(), "启动诊断") ||
		!strings.Contains(response.Body.String(), "/api/gm/stats/startup") ||
		!strings.Contains(response.Body.String(), "SDK 登录成功率") {
		t.Fatal("后台没有提供匿名启动诊断入口")
	}
}

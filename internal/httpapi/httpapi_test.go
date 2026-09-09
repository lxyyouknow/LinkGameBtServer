package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/leaderboard"
	"linkgame-server/internal/player"
)

type leaderboardTestAuthenticator struct{}

func TestBTCloudDisablesGuestLogin(t *testing.T) {
	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{EnableTikTokLogin: true})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/auth/guest", strings.NewReader(`{"installationId":"fixture-device"}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("游客入口应关闭，实际 %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "GUEST_LOGIN_DISABLED") {
		t.Fatal("缺少明确关闭状态")
	}
}

func (leaderboardTestAuthenticator) AuthenticateToken(context.Context, string) (auth.Player, error) {
	return auth.Player{ID: 42, PublicID: "public-player"}, nil
}

type leaderboardTestService struct {
	limit int
	code  string
}

func (service *leaderboardTestService) Global(_ context.Context, playerID uint64, limit int) (leaderboard.Snapshot, error) {
	service.limit = limit
	rank := 1
	entry := leaderboard.Entry{Rank: &rank, DisplayName: "USER000042", PlayerNumber: 42, Level: 7, IsSelf: playerID == 42}
	return leaderboard.Snapshot{Scope: "global", Entries: []leaderboard.Entry{entry}, Self: entry, ServerTime: time.Now()}, nil
}

func (service *leaderboardTestService) AuthorizeTikTokProfile(_ context.Context, playerID uint64, code string) (leaderboard.Profile, error) {
	service.code = code
	if playerID != 42 {
		return leaderboard.Profile{}, leaderboard.ErrIdentityMismatch
	}
	return leaderboard.Profile{Status: "authorized", DisplayName: "PairMaster", UpdatedAt: time.Now()}, nil
}

func TestAdminLatestUsesEmbeddedAdminHandler(t *testing.T) {
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Vary", "*")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("latest-admin"))
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(logger, Dependencies{AdminUI: admin})

	for _, path := range []string{"/admin.html", "/admin", "/admin/latest"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Body.String() != "latest-admin" {
			t.Fatalf("%s 未路由到统计后台: status=%d body=%q", path, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Vary") != "*" {
			t.Fatalf("%s 缺少防缓存 Vary 头", path)
		}
	}
}

func TestWriteJSONDisablesSharedCaching(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, 200, map[string]string{"status": "ok"})

	for _, directive := range []string{"no-store", "s-maxage=0"} {
		if value := recorder.Header().Get("Cache-Control"); !strings.Contains(value, directive) {
			t.Fatalf("Cache-Control=%q，缺少 %q", value, directive)
		}
	}
	if value := recorder.Header().Get("CDN-Cache-Control"); value != "no-store" {
		t.Fatalf("CDN-Cache-Control=%q", value)
	}
	if value := recorder.Header().Get("Cloudflare-CDN-Cache-Control"); value != "no-store" {
		t.Fatalf("Cloudflare-CDN-Cache-Control=%q", value)
	}
	if value := recorder.Header().Get("Vary"); value != "*" {
		t.Fatalf("Vary=%q", value)
	}
}

func TestSavePayload返回跨设备TikTok任务状态(t *testing.T) {
	payload := savePayload(player.Save{
		ClaimedTikTokMissions: []player.TikTokMissionKey{
			player.TikTokMissionHomeShortcut,
			player.TikTokMissionProfileRevisit,
		},
	})
	missions, ok := payload["claimedTikTokMissions"].([]player.TikTokMissionKey)
	if !ok || len(missions) != 2 {
		t.Fatalf("claimedTikTokMissions = %#v", payload["claimedTikTokMissions"])
	}
}

func TestLeaderboardRoutes使用当前会话且返回公开契约(t *testing.T) {
	service := &leaderboardTestService{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(logger, Dependencies{
		SessionAuthenticator: leaderboardTestAuthenticator{}, LeaderboardService: service,
	})

	ranking := httptest.NewRecorder()
	rankingRequest := httptest.NewRequest(http.MethodGet, "/v1/leaderboards/global?limit=2", nil)
	rankingRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(ranking, rankingRequest)
	if ranking.Code != http.StatusOK || service.limit != 2 {
		t.Fatalf("总榜接口失败: status=%d body=%s limit=%d", ranking.Code, ranking.Body.String(), service.limit)
	}
	var rankingPayload struct {
		Scope   string              `json:"scope"`
		Entries []leaderboard.Entry `json:"entries"`
		Self    leaderboard.Entry   `json:"self"`
	}
	if err := json.Unmarshal(ranking.Body.Bytes(), &rankingPayload); err != nil || rankingPayload.Scope != "global" || rankingPayload.Self.PlayerNumber != 42 {
		t.Fatalf("总榜响应不符合契约: payload=%#v err=%v", rankingPayload, err)
	}

	profile := httptest.NewRecorder()
	profileRequest := httptest.NewRequest(http.MethodPost, "/v1/player/profile/tiktok", strings.NewReader(`{"code":"one-time-code"}`))
	profileRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(profile, profileRequest)
	if profile.Code != http.StatusOK || service.code != "one-time-code" {
		t.Fatalf("资料接口失败: status=%d body=%s code=%q", profile.Code, profile.Body.String(), service.code)
	}
}

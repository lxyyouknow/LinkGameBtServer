// Package httpapi 提供 LinkGame HTTP 路由、鉴权、中间件和统一响应。
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"linkgame-server/internal/adpolicy"
	"linkgame-server/internal/adreward"
	"linkgame-server/internal/analytics"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/dailygift"
	"linkgame-server/internal/leaderboard"
	"linkgame-server/internal/platform"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

const requestIDHeader = "X-Request-ID"
const maxJSONBodyBytes int64 = 128 * 1024

type contextKey string

const (
	requestIDContextKey contextKey = "request_id"
	playerContextKey    contextKey = "player"
)

type ReadinessChecker interface{ PingContext(context.Context) error }
type GuestLoginService interface {
	LoginGuest(context.Context, string) (auth.LoginResult, error)
}
type TestAccountLoginService interface {
	LoginTestAccount(context.Context, string) (auth.TestAccountLoginResult, error)
}
type PlatformLoginService interface {
	Login(context.Context, string, string) (platform.LoginResult, error)
}
type SessionAuthenticator interface {
	AuthenticateToken(context.Context, string) (auth.Player, error)
}
type SaveService interface {
	GetSave(context.Context, uint64) (player.Save, error)
	UpdateSave(context.Context, uint64, player.UpdateSaveInput) (player.Save, error)
}
type LeaderboardService interface {
	Global(context.Context, uint64, int) (leaderboard.Snapshot, error)
	AuthorizeTikTokProfile(context.Context, uint64, string) (leaderboard.Profile, error)
}
type DailyGiftService interface {
	GetToday(context.Context, uint64) (dailygift.QueryResult, error)
	Claim(context.Context, uint64, string, string) (dailygift.ClaimResult, error)
}
type DailyChallengeService interface {
	Get(context.Context, uint64) (dailychallenge.State, error)
	Start(context.Context, uint64, string) (dailychallenge.Attempt, error)
	Complete(context.Context, uint64, string, string, int) (dailychallenge.CompleteResult, error)
}
type SeasonService interface {
	Get(context.Context, uint64) (season.State, error)
	RecordLevelClear(context.Context, uint64, string, string, int) (season.ProgressResult, error)
	Claim(context.Context, uint64, season.Task, string, string, string) (season.ClaimResult, error)
	InspectPlayer(context.Context, string) (season.AdminAudit, error)
}
type AdRewardService interface {
	Create(context.Context, uint64, adreward.Placement, string) (adreward.Session, error)
	Claim(context.Context, uint64, string, string, string) (adreward.ClaimResult, error)
}
type AnalyticsService interface {
	Timezone() string
	Ingest(context.Context, uint64, []analytics.Event) ([]string, error)
	IngestStartup(context.Context, []analytics.StartupEvent) ([]string, error)
	StartupDiagnostics(context.Context, analytics.Filter) (analytics.StartupDiagnostics, error)
	Overview(context.Context, analytics.Filter) (analytics.Overview, error)
	OverviewTrend(context.Context, analytics.Filter) ([]analytics.OverviewTrendItem, error)
	Daily(context.Context, analytics.Filter) ([]analytics.DailyItem, error)
	LevelDistribution(context.Context, analytics.Filter) ([]analytics.LevelItem, error)
	Funnel(context.Context, analytics.Filter) ([]analytics.FunnelItem, error)
	ThemeUsage(context.Context, analytics.Filter) ([]analytics.CountItem, error)
	PropUsage(context.Context, analytics.Filter) ([]analytics.CountItem, error)
	LevelRanking(context.Context, analytics.Filter) ([]analytics.RankingItem, error)
	AdRanking(context.Context, analytics.Filter) ([]analytics.AdRankingItem, error)
	AdPerformance(context.Context, analytics.Filter) ([]analytics.AdItem, error)
	AdFailures(context.Context, analytics.Filter) ([]analytics.AdFailureItem, error)
}
type GMService interface {
	Login(string, string) (string, time.Time, error)
	Authenticate(string) bool
	Logout(string)
}

type Dependencies struct {
	AdPolicyPath                    string
	ReadinessChecker                ReadinessChecker
	GuestLoginService               GuestLoginService
	TestAccountLogin                TestAccountLoginService
	EnableTestAccount               bool
	PlatformLogin                   PlatformLoginService
	EnableTikTokLogin               bool
	TikTokHomeShortcutEnabled       bool
	TikTokProfileRevisitEnabled     bool
	TikTokProfileRevisitJumpEnabled bool
	SessionAuthenticator            SessionAuthenticator
	SaveService                     SaveService
	LeaderboardService              LeaderboardService
	DailyGiftService                DailyGiftService
	DailyChallengeService           DailyChallengeService
	SeasonService                   SeasonService
	AdRewardService                 AdRewardService
	AnalyticsService                AnalyticsService
	GMService                       GMService
	AdminUI                         http.Handler
	CORSAllowedOrigins              []string
}

func NewHandler(logger *slog.Logger, dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	if dependencies.AdminUI != nil {
		mux.Handle("/admin.html", dependencies.AdminUI)
		mux.Handle("/admin", dependencies.AdminUI)
		// 永久推荐入口：路径创建后首个响应即携带 Vary: *，不会命中历史 CDN 缓存。
		mux.Handle("/admin/latest", dependencies.AdminUI)
	}
	mux.HandleFunc("/health/live", handleLiveness)
	mux.HandleFunc("/health/ready", handleReadiness(logger, dependencies.ReadinessChecker))
	mux.HandleFunc("/v1/auth/guest", handleGuestLogin(dependencies.GuestLoginService, dependencies.EnableTestAccount))
	mux.HandleFunc("/v1/auth/test-account", handleTestLogin(dependencies.TestAccountLogin, dependencies.EnableTestAccount))
	mux.HandleFunc("/v1/auth/platform", handlePlatformLogin(dependencies.PlatformLogin, dependencies.EnableTikTokLogin))
	mux.HandleFunc("/v1/platform/features", handlePlatformFeatures(dependencies))
	mux.HandleFunc("/v1/ads/policy", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, adpolicy.Read(dependencies.AdPolicyPath))
	})
	mux.Handle("/v1/save", requireSession(dependencies.SessionAuthenticator, handleSave(dependencies.SaveService)))
	mux.Handle("/v1/leaderboards/global", requireSession(dependencies.SessionAuthenticator, handleGlobalLeaderboard(dependencies.LeaderboardService)))
	mux.Handle("/v1/player/profile/tiktok", requireSession(dependencies.SessionAuthenticator, handleTikTokLeaderboardProfile(dependencies.LeaderboardService)))
	mux.Handle("/v1/daily-gift", requireSession(dependencies.SessionAuthenticator, handleDailyGiftQuery(dependencies.DailyGiftService)))
	mux.Handle("/v1/daily-gift/claim", requireSession(dependencies.SessionAuthenticator, handleDailyGiftClaim(dependencies.DailyGiftService)))
	mux.Handle("/v1/daily-challenge", requireSession(dependencies.SessionAuthenticator, handleDailyChallengeQuery(dependencies.DailyChallengeService)))
	mux.Handle("/v1/daily-challenge/start", requireSession(dependencies.SessionAuthenticator, handleDailyChallengeStart(dependencies.DailyChallengeService)))
	mux.Handle("/v1/daily-challenge/complete", requireSession(dependencies.SessionAuthenticator, handleDailyChallengeComplete(dependencies.DailyChallengeService)))
	mux.Handle("/v1/season/current", requireSession(dependencies.SessionAuthenticator, handleSeasonQuery(dependencies.SeasonService)))
	mux.Handle("/v1/season/progress/level-clear", requireSession(dependencies.SessionAuthenticator, handleSeasonLevelClear(dependencies.SeasonService)))
	mux.Handle("/v1/season/tasks/login/claim", requireSession(dependencies.SessionAuthenticator, handleSeasonClaim(dependencies.SeasonService, season.TaskLogin)))
	mux.Handle("/v1/season/tasks/clear_levels/claim", requireSession(dependencies.SessionAuthenticator, handleSeasonClaim(dependencies.SeasonService, season.TaskClearLevels)))
	mux.Handle("/v1/ads/rewarded/session", requireSession(dependencies.SessionAuthenticator, handleAdRewardCreate(dependencies.AdRewardService)))
	mux.Handle("/v1/ads/rewarded/claim", requireSession(dependencies.SessionAuthenticator, handleAdRewardClaim(dependencies.AdRewardService)))
	mux.Handle("/v1/analytics/events", requireSession(dependencies.SessionAuthenticator, handleAnalyticsIngest(dependencies.AnalyticsService)))
	mux.Handle("/v1/analytics/startup", handleStartupDiagnosticsIngest(dependencies.AnalyticsService))
	mux.HandleFunc("/config/timezone", handleTimezone(dependencies.AnalyticsService))
	mux.HandleFunc("/api/gm/login", handleGMLogin(dependencies.GMService))
	mux.HandleFunc("/api/gm/logout", handleGMLogout(dependencies.GMService))
	mux.HandleFunc("/api/gm/stats/overview", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "overview"))
	mux.HandleFunc("/api/gm/stats/overview-trend", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "overview-trend"))
	mux.HandleFunc("/api/gm/stats/daily", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "daily"))
	mux.HandleFunc("/api/gm/stats/levels", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "levels"))
	mux.HandleFunc("/api/gm/stats/funnel", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "funnel"))
	mux.HandleFunc("/api/gm/stats/themes", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "themes"))
	mux.HandleFunc("/api/gm/stats/props", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "props"))
	mux.HandleFunc("/api/gm/stats/ranking", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "ranking"))
	mux.HandleFunc("/api/gm/stats/ad-ranking", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "ad-ranking"))
	mux.HandleFunc("/api/gm/stats/ads", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "ads"))
	mux.HandleFunc("/api/gm/stats/ad-failures", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "ad-failures"))
	mux.HandleFunc("/api/gm/stats/startup", handleGMStats(dependencies.GMService, dependencies.AnalyticsService, "startup"))
	mux.HandleFunc("/api/gm/season/player", handleGMSeasonPlayer(dependencies.GMService, dependencies.SeasonService))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "接口不存在", requestID(r))
	})
	var handler http.Handler = mux
	handler = withCORS(dependencies.CORSAllowedOrigins, handler)
	handler = recoverPanic(logger, handler)
	handler = logRequest(logger, handler)
	handler = assignRequestID(handler)
	return handler
}

func handleGlobalLeaderboard(service LeaderboardService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if service == nil {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "排行榜暂不可用", requestID(r))
			return
		}
		limit := leaderboard.MaxLimit
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				writeError(w, 400, "INVALID_ARGUMENT", "limit 不合法", requestID(r))
				return
			}
			limit = parsed
		}
		result, err := service.Global(r.Context(), playerFromContext(r.Context()).ID, limit)
		if err != nil {
			if errors.Is(err, leaderboard.ErrInvalidLimit) {
				writeError(w, 400, "INVALID_ARGUMENT", "limit 必须为 1～30", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleTikTokLeaderboardProfile(service LeaderboardService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if service == nil {
			writeError(w, 503, "PROFILE_SERVICE_UNAVAILABLE", "TikTok 资料服务暂不可用", requestID(r))
			return
		}
		var body struct {
			Code string `json:"code"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		profile, err := service.AuthorizeTikTokProfile(r.Context(), playerFromContext(r.Context()).ID, body.Code)
		if err != nil {
			status, code, message := leaderboardProfileError(err)
			if status == 500 {
				writeInternal(w, r, err)
				return
			}
			writeError(w, status, code, message, requestID(r))
			return
		}
		writeJSON(w, 200, profile)
	})
}

func leaderboardProfileError(err error) (int, string, string) {
	switch {
	case errors.Is(err, leaderboard.ErrInvalidCode), errors.Is(err, platform.ErrCodeInvalid):
		return 400, "PROFILE_AUTH_CODE_INVALID", "TikTok 授权凭证无效，请重新授权"
	case errors.Is(err, platform.ErrScopeDenied):
		return 422, "PROFILE_SCOPE_REQUIRED", "TikTok 基础资料权限未生效"
	case errors.Is(err, leaderboard.ErrIdentityMismatch):
		return 403, "PROFILE_IDENTITY_MISMATCH", "授权资料与当前账号不一致"
	case errors.Is(err, platform.ErrProfileUnauthorized):
		return 422, "PROFILE_AUTH_REVOKED", "TikTok 基础资料授权已失效"
	case errors.Is(err, leaderboard.ErrProfileUnavailable), errors.Is(err, platform.ErrAuthUnavailable):
		return 503, "PROFILE_SERVICE_UNAVAILABLE", "TikTok 资料服务暂不可用"
	case errors.Is(err, platform.ErrAuthConfiguration):
		return 503, "PROFILE_AUTH_CONFIG_ERROR", "TikTok 资料服务配置错误"
	case errors.Is(err, platform.ErrInvalidResponse):
		return 502, "PROFILE_UPSTREAM_INVALID", "TikTok 资料响应暂不可用"
	default:
		return 500, "INTERNAL_ERROR", "服务内部错误"
	}
}

func handleLiveness(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "requestId": requestID(r)})
}
func handleReadiness(logger *slog.Logger, checker ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		if checker == nil {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "服务暂未就绪", requestID(r))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := checker.PingContext(ctx); err != nil {
			logger.WarnContext(r.Context(), "MySQL 就绪检查失败", "request_id", requestID(r), "error", err)
			writeError(w, 503, "SERVICE_UNAVAILABLE", "服务暂未就绪", requestID(r))
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok", "requestId": requestID(r)})
	}
}

func handleGuestLogin(service GuestLoginService, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		// BT 云环境只接海外 TikTok；游客入口随本地测试账号开关关闭，避免统计混入模拟身份。
		if !enabled || service == nil {
			writeError(w, 404, "GUEST_LOGIN_DISABLED", "当前环境未开放游客登录", requestID(r))
			return
		}
		var body struct {
			InstallationID string `json:"installationId"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.LoginGuest(r.Context(), body.InstallationID)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidInstallationID) {
				writeError(w, 400, "INVALID_ARGUMENT", "installationId 不合法", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeLogin(w, r, result, "", "")
	}
}
func handleTestLogin(service TestAccountLoginService, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !enabled {
			writeError(w, 403, "TEST_LOGIN_DISABLED", "当前环境未开放测试账号登录", requestID(r))
			return
		}
		var body struct {
			Account string `json:"account"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.LoginTestAccount(r.Context(), body.Account)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidTestAccount) {
				writeError(w, 400, "INVALID_ARGUMENT", "测试账号不合法", requestID(r))
				return
			}
			writeInternal(w, r, err)
			return
		}
		writeLogin(w, r, result.LoginResult, "test_account", result.Account)
	}
}
func handlePlatformLogin(service PlatformLoginService, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if !enabled || service == nil {
			writeError(w, 503, "PLATFORM_LOGIN_DISABLED", "TikTok 登录尚未启用", requestID(r))
			return
		}
		var body struct {
			Provider string `json:"provider"`
			Code     string `json:"code"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Login(r.Context(), body.Provider, body.Code)
		if err != nil {
			status, code, message := platformError(err)
			writeError(w, status, code, message, requestID(r))
			return
		}
		writeLogin(w, r, result.LoginResult, result.Provider, "")
	}
}
func writeLogin(w http.ResponseWriter, r *http.Request, result auth.LoginResult, provider, account string) {
	payload := map[string]any{"playerId": result.Player.PublicID, "token": result.Token, "expiresAt": result.ExpiresAt, "requestId": requestID(r)}
	if provider != "" {
		payload["provider"] = provider
	}
	if account != "" {
		payload["account"] = account
	}
	writeJSON(w, 200, payload)
}

func handlePlatformFeatures(d Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"homeShortcutEnabled": d.TikTokHomeShortcutEnabled, "profileRevisitEnabled": d.TikTokProfileRevisitEnabled, "profileRevisitJumpEnabled": d.TikTokProfileRevisitJumpEnabled, "requestId": requestID(r)})
	}
}

type saveRequest struct {
	Revision               uint64                         `json:"revision"`
	Level                  int                            `json:"level"`
	SelectedTheme          int                            `json:"selectedTheme"`
	CollectingTheme        int                            `json:"collectingTheme"`
	SoundEnabled           bool                           `json:"soundEnabled"`
	MusicEnabled           bool                           `json:"musicEnabled"`
	EffectsEnabled         bool                           `json:"effectsEnabled"`
	VibrationEnabled       bool                           `json:"vibrationEnabled"`
	TutorialCompleted      bool                           `json:"tutorialCompleted"`
	CoinMutations          []player.CoinMutation          `json:"coinMutations"`
	PropMutations          []player.PropMutation          `json:"propMutations"`
	ThemeFragmentMutations []player.ThemeFragmentMutation `json:"themeFragmentMutations"`
	ClientVersion          string                         `json:"clientVersion"`
}

func handleSave(service SaveService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "云存档暂不可用", requestID(r))
			return
		}
		switch r.Method {
		case http.MethodGet:
			save, err := service.GetSave(r.Context(), p.ID)
			if err != nil {
				writeInternal(w, r, err)
				return
			}
			writeSave(w, r, save)
		case http.MethodPut:
			var body saveRequest
			if !decodeJSON(w, r, &body) {
				return
			}
			save, err := service.UpdateSave(r.Context(), p.ID, player.UpdateSaveInput{Revision: body.Revision, Level: body.Level, SelectedTheme: body.SelectedTheme, CollectingTheme: body.CollectingTheme, SoundEnabled: body.SoundEnabled, MusicEnabled: body.MusicEnabled, EffectsEnabled: body.EffectsEnabled, VibrationEnabled: body.VibrationEnabled, TutorialCompleted: body.TutorialCompleted, CoinMutations: body.CoinMutations, PropMutations: body.PropMutations, ThemeFragmentMutations: body.ThemeFragmentMutations, ClientVersion: body.ClientVersion})
			if err != nil {
				writeSaveError(w, r, err)
				return
			}
			writeSave(w, r, save)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeError(w, 405, "METHOD_NOT_ALLOWED", "请求方法不支持", requestID(r))
		}
	})
}
func writeSave(w http.ResponseWriter, r *http.Request, s player.Save) {
	payload := savePayload(s)
	payload["requestId"] = requestID(r)
	writeJSON(w, 200, payload)
}

func savePayload(s player.Save) map[string]any {
	return map[string]any{
		"revision":                         s.Revision,
		"level":                            s.Level,
		"selectedTheme":                    s.SelectedTheme,
		"collectingTheme":                  s.CollectingTheme,
		"coins":                            s.Coins,
		"hintCount":                        s.HintCount,
		"shuffleCount":                     s.ShuffleCount,
		"removeCount":                      s.RemoveCount,
		"themeFragments":                   s.ThemeFragments,
		"soundEnabled":                     s.SoundEnabled,
		"musicEnabled":                     s.MusicEnabled,
		"effectsEnabled":                   s.EffectsEnabled,
		"vibrationEnabled":                 s.VibrationEnabled,
		"tutorialCompleted":                s.TutorialCompleted,
		"acceptedCoinMutationIds":          s.AcceptedCoinMutationIDs,
		"acceptedPropMutationIds":          s.AcceptedPropMutationIDs,
		"acceptedThemeFragmentMutationIds": s.AcceptedThemeFragmentMutationIDs,
		"claimedTikTokMissions":            s.ClaimedTikTokMissions,
		"clientVersion":                    s.ClientVersion,
		"updatedAt":                        s.UpdatedAt,
	}
}

func handleDailyGiftQuery(service DailyGiftService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "每日礼包暂不可用", requestID(r))
			return
		}
		result, err := service.GetToday(r.Context(), p.ID)
		if err != nil {
			writeDailyGiftError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"serverTime":  result.ServerTime,
			"dayKey":      result.DayKey,
			"nextResetAt": result.NextResetAt,
			"gift":        result.Gift,
			"requestId":   requestID(r),
		})
	})
}

func handleDailyGiftClaim(service DailyGiftService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "每日礼包暂不可用", requestID(r))
			return
		}
		var body struct {
			OfferID   string `json:"offerId"`
			RequestID string `json:"requestId"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Claim(r.Context(), p.ID, body.OfferID, body.RequestID)
		if err != nil {
			writeDailyGiftError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"gift":           result.Gift,
			"grantedRewards": result.GrantedRewards,
			"save":           savePayload(result.Save),
			"requestId":      requestID(r),
		})
	})
}

func writeDailyGiftError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, dailygift.ErrInvalidRequest):
		writeError(w, 400, "DAILY_GIFT_INVALID_REQUEST", "每日礼包请求不合法", requestID(r))
	case errors.Is(err, dailygift.ErrNotFound):
		writeError(w, 404, "DAILY_GIFT_NOT_FOUND", "每日礼包不存在，请重新查询", requestID(r))
	case errors.Is(err, dailygift.ErrAlreadyClaimed):
		writeError(w, 409, "DAILY_GIFT_ALREADY_CLAIMED", "今日礼包已经领取", requestID(r))
	case errors.Is(err, dailygift.ErrExpired):
		writeError(w, 409, "DAILY_GIFT_EXPIRED", "礼包已经过期，请重新查询", requestID(r))
	case errors.Is(err, dailygift.ErrInsufficientRewards):
		writeError(w, 409, "DAILY_GIFT_REWARDS_UNAVAILABLE", "限定主题碎片不足，礼包暂不可领取", requestID(r))
	case errors.Is(err, dailygift.ErrIdempotencyKeyReused):
		writeError(w, 409, "IDEMPOTENCY_KEY_REUSED", "requestId 已用于其他礼包请求", requestID(r))
	case errors.Is(err, player.ErrPropLimitExceeded):
		writeError(w, 422, "PROP_LIMIT_EXCEEDED", "道具数量已经达到上限", requestID(r))
	case errors.Is(err, player.ErrCoinLimitExceeded):
		writeError(w, 422, "COIN_LIMIT_EXCEEDED", "金币数量已经达到上限", requestID(r))
	default:
		writeInternal(w, r, err)
	}
}
func writeSaveError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, player.ErrRevisionConflict):
		writeError(w, 409, "REVISION_CONFLICT", "云存档已更新，请重新拉取后合并", requestID(r))
	case errors.Is(err, player.ErrInvalidCoinMutation):
		writeError(w, 400, "INVALID_COIN_MUTATION", "金币流水不合法", requestID(r))
	case errors.Is(err, player.ErrInvalidPropMutation):
		writeError(w, 400, "INVALID_PROP_MUTATION", "道具流水不合法", requestID(r))
	case errors.Is(err, player.ErrInvalidThemeMutation):
		writeError(w, 400, "INVALID_THEME_MUTATION", "主题碎片流水不合法", requestID(r))
	case errors.Is(err, player.ErrInsufficientCoins):
		writeError(w, 422, "INSUFFICIENT_COINS", "金币不足", requestID(r))
	case errors.Is(err, player.ErrInsufficientProps):
		writeError(w, 422, "INSUFFICIENT_PROPS", "道具数量不足", requestID(r))
	case errors.Is(err, player.ErrThemeLocked):
		writeError(w, 422, "THEME_LOCKED", "主题尚未完整激活", requestID(r))
	case errors.Is(err, player.ErrInvalidSave):
		writeError(w, 400, "INVALID_ARGUMENT", "存档参数不合法", requestID(r))
	default:
		writeInternal(w, r, err)
	}
}

func requireSession(authenticator SessionAuthenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, 401, "UNAUTHORIZED", "登录状态无效或已过期", requestID(r))
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		p, err := authenticator.AuthenticateToken(r.Context(), token)
		if err != nil {
			status := 401
			code := "UNAUTHORIZED"
			message := "登录状态无效或已过期"
			if errors.Is(err, auth.ErrAccountDisabled) {
				status = 403
				code = "ACCOUNT_DISABLED"
				message = "账号已禁用"
			}
			writeError(w, status, code, message, requestID(r))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), playerContextKey, p)))
	})
}
func playerFromContext(ctx context.Context) auth.Player {
	p, _ := ctx.Value(playerContextKey).(auth.Player)
	return p
}

func method(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}
	w.Header().Set("Allow", allowed)
	writeError(w, 405, "METHOD_NOT_ALLOWED", "请求方法不支持", requestID(r))
	return false
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "INVALID_ARGUMENT", "JSON 参数不合法", requestID(r))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "INVALID_ARGUMENT", "请求体只能包含一个 JSON 对象", requestID(r))
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	// 正式域名前有 CDN；登录令牌、玩家存档和 GM 报表均禁止进入共享缓存。
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0, s-maxage=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	// 即使 CDN 规则错误地忽略源站 TTL，动态账号和报表数据也必须绕过共享缓存。
	w.Header().Set("Vary", "*")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "requestId": requestID}})
}
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "请求处理失败", "request_id", requestID(r), "error", err)
	writeError(w, 500, "INTERNAL_ERROR", "服务内部错误", requestID(r))
}
func platformError(err error) (int, string, string) {
	switch {
	case errors.Is(err, platform.ErrCodeInvalid):
		return 401, "PLATFORM_CODE_INVALID", "TikTok 登录凭证无效，请重新获取"
	case errors.Is(err, platform.ErrAuthConfiguration):
		return 503, "PLATFORM_AUTH_CONFIG_ERROR", "TikTok 登录配置错误"
	case errors.Is(err, platform.ErrAuthUnavailable):
		return 503, "PLATFORM_AUTH_UNAVAILABLE", "TikTok 登录暂不可用"
	case errors.Is(err, platform.ErrUnsupportedProvider), errors.Is(err, platform.ErrInvalidCode):
		return 400, "INVALID_ARGUMENT", "平台登录参数不合法"
	default:
		return 500, "INTERNAL_ERROR", "服务内部错误"
	}
}

func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			copy(buf, []byte(time.Now().Format("150405.000000")))
		}
		id := hex.EncodeToString(buf)
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey, id)))
	})
}
func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDContextKey).(string)
	return id
}
func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}
func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(r.Context(), "HTTP panic", "request_id", requestID(r), "panic", recovered, "stack", string(debug.Stack()))
				writeError(w, 500, "INTERNAL_ERROR", "服务内部错误", requestID(r))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func logRequest(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.InfoContext(r.Context(), "HTTP 请求完成", "request_id", requestID(r), "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

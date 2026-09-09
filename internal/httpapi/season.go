package httpapi

import (
	"errors"
	"net/http"

	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

func handleSeasonQuery(service SeasonService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "赛季暂不可用", requestID(r))
			return
		}
		result, err := service.Get(r.Context(), p.ID)
		if err != nil {
			writeSeasonError(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleSeasonLevelClear(service SeasonService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "赛季暂不可用", requestID(r))
			return
		}
		var body struct {
			RequestID    string `json:"requestId"`
			CompletionID string `json:"completionId"`
			Level        int    `json:"level"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.RecordLevelClear(r.Context(), p.ID, body.RequestID, body.CompletionID, body.Level)
		if err != nil {
			writeSeasonError(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleSeasonClaim(service SeasonService, task season.Task) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "赛季暂不可用", requestID(r))
			return
		}
		var body struct {
			RequestID         string `json:"requestId"`
			ExpectedSeasonKey string `json:"expectedSeasonKey"`
			ExpectedDayKey    string `json:"expectedDayKey"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Claim(r.Context(), p.ID, task, body.RequestID, body.ExpectedSeasonKey, body.ExpectedDayKey)
		if err != nil {
			if errors.Is(err, season.ErrDayChanged) {
				current, currentErr := service.Get(r.Context(), p.ID)
				if currentErr == nil {
					writeJSON(w, http.StatusConflict, map[string]any{
						"error": map[string]any{
							"code": "SEASON_DAY_CHANGED", "message": "赛季日期已经变化，请重新查询", "requestId": requestID(r),
						},
						"season": current,
					})
					return
				}
			}
			writeSeasonError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"acceptedTask":   result.AcceptedTask,
			"grantedRewards": result.GrantedRewards,
			"season":         result.Season,
			"save":           savePayload(result.Save),
			"requestId":      requestID(r),
		})
	})
}

func writeSeasonError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, season.ErrInvalidRequest):
		writeError(w, 400, "SEASON_INVALID_REQUEST", "赛季请求不合法", requestID(r))
	case errors.Is(err, season.ErrDayChanged):
		writeError(w, 409, "SEASON_DAY_CHANGED", "赛季日期已经变化，请重新查询", requestID(r))
	case errors.Is(err, season.ErrRewardPeriodClosed):
		writeError(w, 409, "SEASON_REWARD_PERIOD_CLOSED", "本月赛季奖励期已经结束", requestID(r))
	case errors.Is(err, season.ErrTaskNotReady):
		writeError(w, 409, "SEASON_TASK_NOT_READY", "赛季任务尚未完成", requestID(r))
	case errors.Is(err, season.ErrTaskAlreadyClaimed):
		writeError(w, 409, "SEASON_TASK_ALREADY_CLAIMED", "赛季任务已经领取", requestID(r))
	case errors.Is(err, season.ErrDayAlreadyRewarded):
		writeError(w, 409, "SEASON_DAY_ALREADY_REWARDED", "赛季当天奖励已经领取", requestID(r))
	case errors.Is(err, season.ErrConfigUnavailable):
		writeError(w, 503, "SEASON_CONFIG_UNAVAILABLE", "赛季配置暂不可用", requestID(r))
	case errors.Is(err, season.ErrRequestIDConflict):
		writeError(w, 409, "REQUEST_ID_CONFLICT", "requestId 已用于其他赛季请求", requestID(r))
	case errors.Is(err, season.ErrStateConflict):
		writeError(w, 409, "SEASON_STATE_CONFLICT", "赛季状态已经变化，请重新查询", requestID(r))
	case errors.Is(err, season.ErrPlayerNotFound):
		writeError(w, 404, "SEASON_PLAYER_NOT_FOUND", "未找到该玩家", requestID(r))
	case errors.Is(err, player.ErrPropLimitExceeded):
		writeError(w, 422, "PROP_LIMIT_EXCEEDED", "道具数量已经达到上限", requestID(r))
	default:
		writeInternal(w, r, err)
	}
}

func handleGMSeasonPlayer(gmService GMService, service SeasonService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		if gmService == nil || !gmService.Authenticate(bearer(r)) {
			writeError(w, 401, "GM_UNAUTHORIZED", "后台登录已失效", requestID(r))
			return
		}
		if service == nil {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "赛季审计暂不可用", requestID(r))
			return
		}
		var body struct {
			PlayerID string `json:"playerId"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.InspectPlayer(r.Context(), body.PlayerID)
		if err != nil {
			writeSeasonError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": result, "requestId": requestID(r)})
	}
}

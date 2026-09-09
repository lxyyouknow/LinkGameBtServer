package httpapi

import (
	"errors"
	"net/http"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

func handleAdRewardCreate(service AdRewardService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		player := playerFromContext(r.Context())
		if service == nil || player.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "广告奖励服务暂不可用", requestID(r))
			return
		}
		var body struct {
			Placement   adreward.Placement `json:"placement"`
			BusinessKey string             `json:"businessKey"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Create(r.Context(), player.ID, body.Placement, body.BusinessKey)
		if err != nil {
			writeAdRewardError(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleAdRewardClaim(service AdRewardService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		player := playerFromContext(r.Context())
		if service == nil || player.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "广告奖励服务暂不可用", requestID(r))
			return
		}
		var body struct {
			SessionID   string `json:"sessionId"`
			RequestID   string `json:"requestId"`
			AdAttemptID string `json:"adAttemptId"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Claim(r.Context(), player.ID, body.SessionID, body.RequestID, body.AdAttemptID)
		if err != nil {
			writeAdRewardError(w, r, err)
			return
		}
		// player.Save 是服务端内部结构，没有 JSON tag。不能直接序列化，否则字段会变成
		// Save.Revision / Save.HintCount 等 PascalCase，客户端按云存档契约读取时会把库存归零。
		// 广告核销必须与 GET /v1/save、每日礼包复用同一套 camelCase 快照协议。
		payload := map[string]any{
			"sessionId":      result.SessionID,
			"placement":      result.Placement,
			"grantedRewards": result.GrantedRewards,
			"save":           savePayload(result.Save),
			"requestId":      requestID(r),
		}
		if result.Challenge != nil {
			payload["challenge"] = result.Challenge
		}
		if result.Season != nil {
			payload["season"] = result.Season
		}
		writeJSON(w, 200, payload)
	})
}

func writeAdRewardError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, adreward.ErrInvalidRequest):
		writeError(w, 400, "AD_REWARD_INVALID", "广告奖励请求不合法", requestID(r))
	case errors.Is(err, adreward.ErrNotFound):
		writeError(w, 404, "AD_REWARD_NOT_FOUND", "广告奖励会话不存在", requestID(r))
	case errors.Is(err, adreward.ErrExpired):
		writeError(w, 409, "AD_REWARD_EXPIRED", "广告奖励会话已过期，请重新观看", requestID(r))
	case errors.Is(err, adreward.ErrAlreadyClaimed):
		writeError(w, 409, "AD_REWARD_ALREADY_CLAIMED", "广告奖励已经领取", requestID(r))
	case errors.Is(err, adreward.ErrIdempotencyKeyReused):
		writeError(w, 409, "AD_REWARD_REQUEST_REUSED", "广告奖励请求号已被使用", requestID(r))
	case errors.Is(err, adreward.ErrBusinessState):
		writeError(w, 409, "AD_REWARD_STATE_INVALID", "当前进度不满足广告奖励条件", requestID(r))
	case errors.Is(err, dailychallenge.ErrLocked):
		writeError(w, 409, "DAILY_CHALLENGE_LOCKED", "每日挑战尚未解锁", requestID(r))
	case errors.Is(err, dailychallenge.ErrReplayAlreadyAvailable):
		writeError(w, 409, "DAILY_CHALLENGE_REPLAY_ALREADY_AVAILABLE", "再次挑战资格已经存在", requestID(r))
	case errors.Is(err, dailychallenge.ErrExpired):
		writeError(w, 409, "DAILY_CHALLENGE_EXPIRED", "每日挑战广告资格已经跨日失效", requestID(r))
	case errors.Is(err, dailychallenge.ErrStateConflict):
		writeError(w, 409, "DAILY_CHALLENGE_STATE_CONFLICT", "每日挑战状态已经变化，请重新查询", requestID(r))
	case errors.Is(err, season.ErrMakeupLimitReached):
		writeError(w, 409, "SEASON_MAKEUP_LIMIT_REACHED", "赛季补签次数已用完", requestID(r))
	case errors.Is(err, season.ErrMakeupDayNotEligible):
		writeError(w, 409, "SEASON_MAKEUP_DAY_NOT_ELIGIBLE", "该日期不能补签", requestID(r))
	case errors.Is(err, season.ErrDayAlreadyRewarded):
		writeError(w, 409, "SEASON_DAY_ALREADY_REWARDED", "该日期奖励已经领取", requestID(r))
	case errors.Is(err, season.ErrRewardPeriodClosed):
		writeError(w, 409, "SEASON_REWARD_PERIOD_CLOSED", "赛季补签会话已经跨月失效", requestID(r))
	case errors.Is(err, season.ErrConfigUnavailable):
		writeError(w, 503, "SEASON_CONFIG_UNAVAILABLE", "赛季配置暂不可用", requestID(r))
	case errors.Is(err, player.ErrPropLimitExceeded):
		writeError(w, 422, "PROP_LIMIT_EXCEEDED", "道具数量已经达到上限", requestID(r))
	default:
		writeInternal(w, r, err)
	}
}

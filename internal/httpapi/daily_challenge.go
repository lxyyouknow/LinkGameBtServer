package httpapi

import (
	"errors"
	"net/http"

	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/player"
)

func handleDailyChallengeQuery(service DailyChallengeService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodGet) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "每日挑战暂不可用", requestID(r))
			return
		}
		result, err := service.Get(r.Context(), p.ID)
		if err != nil {
			writeDailyChallengeError(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleDailyChallengeStart(service DailyChallengeService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "每日挑战暂不可用", requestID(r))
			return
		}
		var body struct {
			RequestID string `json:"requestId"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Start(r.Context(), p.ID, body.RequestID)
		if err != nil {
			writeDailyChallengeError(w, r, err)
			return
		}
		writeJSON(w, 200, result)
	})
}

func handleDailyChallengeComplete(service DailyChallengeService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, http.MethodPost) {
			return
		}
		p := playerFromContext(r.Context())
		if service == nil || p.ID == 0 {
			writeError(w, 503, "SERVICE_UNAVAILABLE", "每日挑战暂不可用", requestID(r))
			return
		}
		var body struct {
			AttemptID    string `json:"attemptId"`
			RequestID    string `json:"requestId"`
			ClearSeconds int    `json:"clearSeconds"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := service.Complete(r.Context(), p.ID, body.AttemptID, body.RequestID, body.ClearSeconds)
		if err != nil {
			writeDailyChallengeError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"attemptId":      result.AttemptID,
			"grantedRewards": result.GrantedRewards,
			"challenge":      result.Challenge,
			"save":           savePayload(result.Save),
			"requestId":      requestID(r),
		})
	})
}

func writeDailyChallengeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, dailychallenge.ErrInvalidRequest):
		writeError(w, 400, "DAILY_CHALLENGE_INVALID_REQUEST", "每日挑战请求不合法", requestID(r))
	case errors.Is(err, dailychallenge.ErrLocked):
		writeError(w, 409, "DAILY_CHALLENGE_LOCKED", "主线进度达到 LEVEL 10 后解锁", requestID(r))
	case errors.Is(err, dailychallenge.ErrReplayRequired):
		writeError(w, 409, "DAILY_CHALLENGE_REPLAY_REQUIRED", "需要完整观看广告后再次挑战", requestID(r))
	case errors.Is(err, dailychallenge.ErrReplayAlreadyAvailable):
		writeError(w, 409, "DAILY_CHALLENGE_REPLAY_ALREADY_AVAILABLE", "再次挑战资格已经存在", requestID(r))
	case errors.Is(err, dailychallenge.ErrAttemptNotFound):
		writeError(w, 404, "DAILY_CHALLENGE_ATTEMPT_NOT_FOUND", "每日挑战轮次不存在", requestID(r))
	case errors.Is(err, dailychallenge.ErrAttemptCompleted):
		writeError(w, 409, "DAILY_CHALLENGE_ATTEMPT_COMPLETED", "每日挑战轮次已经结算", requestID(r))
	case errors.Is(err, dailychallenge.ErrExpired):
		writeError(w, 409, "DAILY_CHALLENGE_EXPIRED", "每日挑战轮次或资格已经跨日失效", requestID(r))
	case errors.Is(err, dailychallenge.ErrStateConflict):
		writeError(w, 409, "DAILY_CHALLENGE_STATE_CONFLICT", "每日挑战状态已经变化，请重新查询", requestID(r))
	case errors.Is(err, player.ErrCoinLimitExceeded):
		writeError(w, 422, "COIN_LIMIT_EXCEEDED", "金币数量已经达到上限", requestID(r))
	default:
		writeInternal(w, r, err)
	}
}

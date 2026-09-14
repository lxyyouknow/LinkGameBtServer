package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

type fakeAdRewardService struct {
	playerID    uint64
	sessionID   string
	requestID   string
	adAttemptID string
	claimResult *adreward.ClaimResult
	claimErr    error
}

func (service *fakeAdRewardService) Create(
	_ context.Context,
	_ uint64,
	_ adreward.Placement,
	_ string,
) (adreward.Session, error) {
	return adreward.Session{}, nil
}

func (service *fakeAdRewardService) Claim(
	_ context.Context,
	playerID uint64,
	sessionID string,
	requestID string,
	adAttemptID string,
) (adreward.ClaimResult, error) {
	service.playerID = playerID
	service.sessionID = sessionID
	service.requestID = requestID
	service.adAttemptID = adAttemptID
	if service.claimErr != nil {
		return adreward.ClaimResult{}, service.claimErr
	}
	if service.claimResult != nil {
		return *service.claimResult, nil
	}
	return adreward.ClaimResult{
		SessionID: sessionID,
		Placement: adreward.PlacementHint,
		GrantedRewards: []adreward.Reward{{
			Type:     "prop",
			PropType: player.PropTypeHint,
			Quantity: 1,
		}},
		Save: player.Save{Revision: 7, HintCount: 3, ShuffleCount: 1, RemoveCount: 2},
	}, nil
}

func TestAdRewardClaim赛季补签返回完整赛季状态(t *testing.T) {
	service := &fakeAdRewardService{claimResult: &adreward.ClaimResult{
		SessionID: "ad_makeup", Placement: adreward.PlacementSeasonMakeup,
		Save:   player.Save{Revision: 2},
		Season: &season.State{SeasonKey: "2026-09", MakeupRemaining: 4, ClaimedDays: []int{1}},
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/ads/rewarded/claim", strings.NewReader(
		`{"sessionId":"ad_makeup","requestId":"ad-claim:makeup","adAttemptId":"ad:makeup"}`,
	))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleAdRewardClaim(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Season season.State `json:"season"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Season.MakeupRemaining != 4 ||
		!reflect.DeepEqual(body.Season.ClaimedDays, []int{1}) {
		t.Fatalf("response=%#v err=%v", body, err)
	}
}

func TestAdRewardClaim赛季补签错误码稳定(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{season.ErrMakeupLimitReached, "SEASON_MAKEUP_LIMIT_REACHED"},
		{season.ErrMakeupDayNotEligible, "SEASON_MAKEUP_DAY_NOT_ELIGIBLE"},
		{season.ErrDayAlreadyRewarded, "SEASON_DAY_ALREADY_REWARDED"},
		{season.ErrRewardPeriodClosed, "SEASON_REWARD_PERIOD_CLOSED"},
	} {
		service := &fakeAdRewardService{claimErr: test.err}
		request := httptest.NewRequest(http.MethodPost, "/v1/ads/rewarded/claim", strings.NewReader(
			`{"sessionId":"ad_makeup","requestId":"ad-claim:makeup","adAttemptId":"ad:makeup"}`,
		))
		request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
		recorder := httptest.NewRecorder()
		handleAdRewardClaim(service).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), test.code) {
			t.Fatalf("err=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAdRewardClaim使用统一云存档字段契约(t *testing.T) {
	service := &fakeAdRewardService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/ads/rewarded/claim",
		strings.NewReader(`{"sessionId":"ad_test","requestId":"ad-claim:test","adAttemptId":"ad:test"}`),
	)
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()

	handleAdRewardClaim(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.playerID != 42 ||
		service.sessionID != "ad_test" || service.requestID != "ad-claim:test" || service.adAttemptID != "ad:test" {
		t.Fatalf("status/service = %d/%#v", recorder.Code, service)
	}
	var body struct {
		SessionID string `json:"sessionId"`
		Save      struct {
			Revision     uint64 `json:"revision"`
			HintCount    int64  `json:"hintCount"`
			ShuffleCount int64  `json:"shuffleCount"`
			RemoveCount  int64  `json:"removeCount"`
		} `json:"save"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.SessionID != "ad_test" || body.Save.Revision != 7 || body.Save.HintCount != 3 ||
		body.Save.ShuffleCount != 1 || body.Save.RemoveCount != 2 {
		t.Fatalf("response = %#v", body)
	}
	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("解析原始响应失败: %v", err)
	}
	save, ok := raw["save"].(map[string]any)
	if !ok {
		t.Fatalf("save 字段不存在: %s", recorder.Body.String())
	}
	if _, leaked := save["HintCount"]; leaked {
		t.Fatalf("响应泄漏了内部 PascalCase 字段: %s", recorder.Body.String())
	}
}

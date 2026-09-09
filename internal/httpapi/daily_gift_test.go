package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/dailygift"
	"linkgame-server/internal/player"
)

type fakeDailyGiftService struct {
	playerID  uint64
	offerID   string
	requestID string
	err       error
}

func (service *fakeDailyGiftService) GetToday(
	_ context.Context,
	playerID uint64,
) (dailygift.QueryResult, error) {
	service.playerID = playerID
	if service.err != nil {
		return dailygift.QueryResult{}, service.err
	}
	return dailygift.QueryResult{
		ServerTime:  time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC),
		DayKey:      "2026-08-25",
		NextResetAt: time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC),
		Gift: dailygift.Gift{
			OfferID: "dg_20260825_test",
			State:   "available",
			BaseRewards: []dailygift.Reward{{
				Type:     "prop",
				PropType: player.PropTypeShuffle,
				Quantity: 1,
			}},
		},
	}, nil
}

func (service *fakeDailyGiftService) Claim(
	_ context.Context,
	playerID uint64,
	offerID string,
	requestID string,
) (dailygift.ClaimResult, error) {
	service.playerID = playerID
	service.offerID = offerID
	service.requestID = requestID
	if service.err != nil {
		return dailygift.ClaimResult{}, service.err
	}
	return dailygift.ClaimResult{
		Gift: dailygift.Gift{OfferID: offerID, State: "claimed"},
		GrantedRewards: []dailygift.Reward{{
			Type:     "prop",
			PropType: player.PropTypeShuffle,
			Quantity: 1,
		}},
		Save: player.Save{Revision: 2, ShuffleCount: 1},
	}, nil
}

func TestDailyGiftQuery使用Token中的玩家(t *testing.T) {
	service := &fakeDailyGiftService{}
	request := httptest.NewRequest(http.MethodGet, "/v1/daily-gift", nil)
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()

	handleDailyGiftQuery(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.playerID != 42 {
		t.Fatalf("status/player = %d/%d", recorder.Code, service.playerID)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body["dayKey"] != "2026-08-25" {
		t.Fatalf("dayKey = %#v", body["dayKey"])
	}
}

func TestDailyGiftClaim只接受服务端礼包标识和幂等键(t *testing.T) {
	service := &fakeDailyGiftService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/daily-gift/claim",
		strings.NewReader(`{"offerId":"dg_20260825_test","requestId":"dg-claim:test"}`),
	)
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 7}))
	recorder := httptest.NewRecorder()

	handleDailyGiftClaim(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.playerID != 7 ||
		service.offerID != "dg_20260825_test" || service.requestID != "dg-claim:test" {
		t.Fatalf("status/service = %d/%#v", recorder.Code, service)
	}
	var body struct {
		Save struct {
			Revision     uint64 `json:"revision"`
			ShuffleCount int64  `json:"shuffleCount"`
		} `json:"save"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Save.Revision != 2 || body.Save.ShuffleCount != 1 {
		t.Fatalf("save = %#v", body.Save)
	}
}

func TestDailyGiftExpired返回稳定错误码(t *testing.T) {
	service := &fakeDailyGiftService{err: dailygift.ErrExpired}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/daily-gift/claim",
		strings.NewReader(`{"offerId":"dg_20260824_test","requestId":"dg-claim:test"}`),
	)
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 7}))
	recorder := httptest.NewRecorder()

	handleDailyGiftClaim(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "DAILY_GIFT_EXPIRED") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDailyGiftErrorMapping不吞掉未知错误(t *testing.T) {
	service := &fakeDailyGiftService{err: errors.New("database unavailable")}
	request := httptest.NewRequest(http.MethodGet, "/v1/daily-gift", nil)
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 7}))
	recorder := httptest.NewRecorder()

	handleDailyGiftQuery(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestDailyGift金币达到上限返回业务错误(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/daily-gift/claim", nil)
	recorder := httptest.NewRecorder()

	writeDailyGiftError(recorder, request, player.ErrCoinLimitExceeded)

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "COIN_LIMIT_EXCEEDED") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdReward道具达到上限返回业务错误(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/ads/rewarded/claim", nil)
	recorder := httptest.NewRecorder()

	writeAdRewardError(recorder, request, player.ErrPropLimitExceeded)

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "PROP_LIMIT_EXCEEDED") {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

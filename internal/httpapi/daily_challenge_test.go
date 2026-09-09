package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/dailychallenge"
)

type fakeDailyChallengeService struct {
	playerID     uint64
	requestID    string
	attemptID    string
	clearSeconds int
}

func (service *fakeDailyChallengeService) Get(_ context.Context, playerID uint64) (dailychallenge.State, error) {
	service.playerID = playerID
	return dailychallenge.State{DayKey: "2026-08-31", Unlocked: true, ChallengeIndex: 3}, nil
}

func (service *fakeDailyChallengeService) Start(_ context.Context, playerID uint64, requestID string) (dailychallenge.Attempt, error) {
	service.playerID = playerID
	service.requestID = requestID
	return dailychallenge.Attempt{AttemptID: "dc_test", DayKey: "2026-08-31", ChallengeIndex: 3, Mode: "free", Status: "active"}, nil
}

func (service *fakeDailyChallengeService) Complete(_ context.Context, playerID uint64, attemptID, requestID string, clearSeconds int) (dailychallenge.CompleteResult, error) {
	service.playerID = playerID
	service.attemptID = attemptID
	service.requestID = requestID
	service.clearSeconds = clearSeconds
	return dailychallenge.CompleteResult{AttemptID: attemptID, Challenge: dailychallenge.State{DayKey: "2026-08-31", ChallengeLevel: 4}}, nil
}

func TestDailyChallengeStart只接受请求号(t *testing.T) {
	service := &fakeDailyChallengeService{}
	request := httptest.NewRequest(http.MethodPost, "/v1/daily-challenge/start", strings.NewReader(`{"requestId":"daily-start:test"}`))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleDailyChallengeStart(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.playerID != 42 || service.requestID != "daily-start:test" {
		t.Fatalf("status/service=%d/%#v body=%s", recorder.Code, service, recorder.Body.String())
	}
	var response dailychallenge.Attempt
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.AttemptID != "dc_test" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestDailyChallengeComplete不接受客户端奖励(t *testing.T) {
	service := &fakeDailyChallengeService{}
	request := httptest.NewRequest(http.MethodPost, "/v1/daily-challenge/complete", strings.NewReader(`{"attemptId":"dc_test","requestId":"daily-complete:test","clearSeconds":86,"rewards":[]}`))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleDailyChallengeComplete(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || service.attemptID != "" {
		t.Fatalf("status/service=%d/%#v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

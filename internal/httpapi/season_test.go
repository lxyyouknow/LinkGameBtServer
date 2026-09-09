package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

type fakeSeasonService struct {
	playerID          uint64
	requestID         string
	completionID      string
	level             int
	task              season.Task
	expectedSeasonKey string
	expectedDayKey    string
	claimErr          error
}

type fakeSeasonGMService struct{}

func (*fakeSeasonGMService) Login(string, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (*fakeSeasonGMService) Authenticate(token string) bool { return token == "gm-session" }
func (*fakeSeasonGMService) Logout(string)                  {}

func (service *fakeSeasonService) Get(_ context.Context, playerID uint64) (season.State, error) {
	service.playerID = playerID
	return season.State{SeasonKey: "2026-09", ConfigID: 4, ClaimedDays: []int{}, RewardDays: []season.RewardDay{}}, nil
}

func (service *fakeSeasonService) RecordLevelClear(_ context.Context, playerID uint64, requestID, completionID string, level int) (season.ProgressResult, error) {
	service.playerID = playerID
	service.requestID = requestID
	service.completionID = completionID
	service.level = level
	return season.ProgressResult{SeasonKey: "2026-09", DayKey: "2026-09-01", ClearedLevels: 1}, nil
}

func (service *fakeSeasonService) Claim(_ context.Context, playerID uint64, task season.Task, requestID, expectedSeasonKey, expectedDayKey string) (season.ClaimResult, error) {
	service.playerID = playerID
	service.task = task
	service.requestID = requestID
	service.expectedSeasonKey = expectedSeasonKey
	service.expectedDayKey = expectedDayKey
	if service.claimErr != nil {
		return season.ClaimResult{}, service.claimErr
	}
	return season.ClaimResult{
		AcceptedTask:   task,
		GrantedRewards: []season.Reward{{Type: "prop", PropType: "hint", Quantity: 3}},
		Season:         season.ClaimState{SeasonKey: expectedSeasonKey, DayKey: expectedDayKey},
		Save:           player.Save{Revision: 9, HintCount: 3, UpdatedAt: time.Now()},
	}, nil
}

func TestSeason跨日领取错误返回当前权威状态(t *testing.T) {
	service := &fakeSeasonService{claimErr: season.ErrDayChanged}
	request := httptest.NewRequest(http.MethodPost, "/v1/season/tasks/login/claim", strings.NewReader(
		`{"requestId":"season-claim:old-day","expectedSeasonKey":"2026-08","expectedDayKey":"2026-08-31"}`,
	))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleSeasonClaim(service, season.TaskLogin).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Season season.State `json:"season"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error.Code != "SEASON_DAY_CHANGED" || body.Season.SeasonKey != "2026-09" {
		t.Fatalf("response=%#v err=%v", body, err)
	}
}

func (service *fakeSeasonService) InspectPlayer(_ context.Context, publicPlayerID string) (season.AdminAudit, error) {
	return season.AdminAudit{PlayerID: publicPlayerID}, nil
}

func TestSeasonLevelClear不接受客户端任务进度(t *testing.T) {
	service := &fakeSeasonService{}
	request := httptest.NewRequest(http.MethodPost, "/v1/season/progress/level-clear", strings.NewReader(
		`{"requestId":"season-clear:1","completionId":"main:1","level":3,"clearedLevels":5}`,
	))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleSeasonLevelClear(service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || service.completionID != "" {
		t.Fatalf("status/service=%d/%#v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestSeasonClaim返回统一云存档结构(t *testing.T) {
	service := &fakeSeasonService{}
	request := httptest.NewRequest(http.MethodPost, "/v1/season/tasks/login/claim", strings.NewReader(
		`{"requestId":"season-claim:1","expectedSeasonKey":"2026-09","expectedDayKey":"2026-09-01"}`,
	))
	request = request.WithContext(context.WithValue(request.Context(), playerContextKey, auth.Player{ID: 42}))
	recorder := httptest.NewRecorder()
	handleSeasonClaim(service, season.TaskLogin).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.task != season.TaskLogin || service.playerID != 42 {
		t.Fatalf("status/service=%d/%#v body=%s", recorder.Code, service, recorder.Body.String())
	}
	var body struct {
		AcceptedTask season.Task `json:"acceptedTask"`
		Save         struct {
			Revision  uint64 `json:"revision"`
			HintCount int64  `json:"hintCount"`
		} `json:"save"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.AcceptedTask != season.TaskLogin || body.Save.Revision != 9 || body.Save.HintCount != 3 {
		t.Fatalf("response=%#v err=%v", body, err)
	}
}

func TestGMSeasonPlayer必须通过后台会话(t *testing.T) {
	service := &fakeSeasonService{}
	request := httptest.NewRequest(http.MethodPost, "/api/gm/season/player", strings.NewReader(
		`{"playerId":"01010101-0101-4101-8101-010101010101"}`,
	))
	request.Header.Set("Authorization", "Bearer gm-session")
	recorder := httptest.NewRecorder()
	handleGMSeasonPlayer(&fakeSeasonGMService{}, service).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/gm/season/player", strings.NewReader(
		`{"playerId":"01010101-0101-4101-8101-010101010101"}`,
	))
	unauthorizedRecorder := httptest.NewRecorder()
	handleGMSeasonPlayer(&fakeSeasonGMService{}, service).ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("未授权 status=%d body=%s", unauthorizedRecorder.Code, unauthorizedRecorder.Body.String())
	}
}

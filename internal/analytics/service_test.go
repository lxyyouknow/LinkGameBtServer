package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkgame-server/internal/player"
)

type stubStore struct{}

type dailyFilterStore struct {
	stubStore
	filter Filter
}

func (store *dailyFilterStore) Daily(_ context.Context, filter Filter) ([]DailyItem, error) {
	store.filter = filter
	return nil, nil
}

func TestDaily按筛选时区传递自然日偏移(t *testing.T) {
	location, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 11, 0, 0, 0, 0, location)
	store := &dailyFilterStore{}
	service := NewService(store, location)
	if _, err := service.Daily(context.Background(), Filter{From: from, To: from.Add(24 * time.Hour), AppID: 1}); err != nil {
		t.Fatal(err)
	}
	if store.filter.ReportUTCOffsetMinutes != 540 || !store.filter.From.Equal(from) || !store.filter.To.Equal(from.Add(24*time.Hour)) {
		t.Fatalf("每日报表时区或日期边界错误: %+v", store.filter)
	}
}

func (stubStore) Ingest(_ context.Context, _ uint64, events []Event, _ time.Time, _ string) ([]string, error) {
	ids := make([]string, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	return ids, nil
}
func (stubStore) IngestStartup(_ context.Context, events []StartupEvent, _ time.Time) ([]string, error) {
	ids := make([]string, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	return ids, nil
}
func (stubStore) StartupDiagnostics(context.Context, Filter) (StartupDiagnostics, error) {
	return StartupDiagnostics{}, nil
}
func (stubStore) Overview(context.Context, Filter) (Overview, error) { return Overview{}, nil }
func (stubStore) OverviewTrend(context.Context, Filter) ([]OverviewTrendItem, error) {
	return nil, nil
}
func (stubStore) Daily(context.Context, Filter) ([]DailyItem, error)             { return nil, nil }
func (stubStore) LevelDistribution(context.Context, Filter) ([]LevelItem, error) { return nil, nil }
func (stubStore) Funnel(context.Context, Filter) ([]FunnelItem, error)           { return nil, nil }
func (stubStore) ThemeUsage(context.Context, Filter) ([]CountItem, error)        { return nil, nil }
func (stubStore) PropUsage(context.Context, Filter) ([]CountItem, error)         { return nil, nil }
func (stubStore) LevelRanking(context.Context, Filter) ([]RankingItem, error)    { return nil, nil }
func (stubStore) AdRanking(context.Context, Filter) ([]AdRankingItem, error)     { return nil, nil }
func (stubStore) AdPerformance(context.Context, Filter) ([]AdItem, error)        { return nil, nil }
func (stubStore) AdFailures(context.Context, Filter) ([]AdFailureItem, error)    { return nil, nil }

func baseEvent(name EventName) Event {
	return Event{ID: "event-1", Name: name, EventTime: "2026-08-20T00:00:00Z", AppID: 1, SDKType: 0, Channel: 0, GameSessionID: "session-123"}
}

func TestIngestStartup接受匿名SDK失败且拒绝敏感字符(t *testing.T) {
	duration := 820
	event := StartupEvent{
		ID: "startup:event-1", LaunchID: "startup:launch-1", Stage: StartupSDKFail,
		EventTime: "2026-09-08T00:00:00Z", AppID: 1, SDKType: 1000, Channel: 0,
		ClientVersion: "0.1.4", Platform: "tiktok", OS: "ios", System: "iOS_18.1",
		TikTokVersion: "45.6.0", SDKVersion: "0.26.0", ErrorCode: "LOGIN_FAILED", DurationMS: &duration,
	}
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.IngestStartup(context.Background(), []StartupEvent{event}); err != nil {
		t.Fatal(err)
	}
	event.ErrorCode = "secret value with spaces"
	if _, err := service.IngestStartup(context.Background(), []StartupEvent{event}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}

func TestIngest接受首个成功配对(t *testing.T) {
	level := 0
	event := baseEvent(EventFirstMatch)
	event.Level = &level
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}
func TestIngest接受主线开始事件(t *testing.T) {
	event := baseEvent(EventMainLevelStart)
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}
func TestIngest接受广告成功事件(t *testing.T) {
	event := baseEvent(EventAdSuccess)
	event.SDKType = 1000
	event.AdFormat = "rewarded"
	event.AdPlacement = "hint"
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}
func TestIngest广告失败必须有错误分类(t *testing.T) {
	event := baseEvent(EventAdFail)
	event.AdFormat = "rewarded"
	event.AdPlacement = "hint"
	service := NewService(stubStore{}, time.UTC)
	_, err := service.Ingest(context.Background(), 1, []Event{event})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}
func TestIngest接受可串联广告漏斗(t *testing.T) {
	duration := 320
	preloaded := true
	event := baseEvent(EventAdLoad)
	event.SDKType = 1000
	event.ClientVersion = "0.1.0"
	event.Platform = "tiktok"
	event.AdFormat = "rewarded"
	event.AdPlacement = "daily_gift"
	event.AdAttemptID = "ad:attempt-1"
	event.AdDurationMS = &duration
	event.AdPreloaded = &preloaded
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}
func TestIngest平台必须与SDK类型一致(t *testing.T) {
	event := baseEvent(EventSessionStart)
	event.SDKType = 0
	event.Platform = "tiktok"
	service := NewService(stubStore{}, time.UTC)
	_, err := service.Ingest(context.Background(), 1, []Event{event})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}
func TestIngest广告关闭必须有结果(t *testing.T) {
	event := baseEvent(EventAdClose)
	event.AdFormat = "rewarded"
	event.AdPlacement = "hint"
	event.AdAttemptID = "ad:attempt-1"
	service := NewService(stubStore{}, time.UTC)
	_, err := service.Ingest(context.Background(), 1, []Event{event})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}
func TestIngest奖励核销失败必须标记广告尝试(t *testing.T) {
	event := baseEvent(EventAdClaimFail)
	event.AdFormat = "rewarded"
	event.AdPlacement = "level_complete"
	event.AdAttemptID = "ad:attempt-1"
	event.AdFailureStage = "reward_claim"
	event.AdErrorCode = "AD_REWARD_CLAIM_FAILED"
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}
func TestIngest奖励核销失败必须使用核销阶段(t *testing.T) {
	event := baseEvent(EventAdClaimFail)
	event.AdFormat = "rewarded"
	event.AdPlacement = "level_complete"
	event.AdAttemptID = "ad:attempt-1"
	event.AdFailureStage = "show"
	event.AdErrorCode = "AD_REWARD_CLAIM_FAILED"
	service := NewService(stubStore{}, time.UTC)
	_, err := service.Ingest(context.Background(), 1, []Event{event})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}
func TestIngest插屏关闭结果不能使用激励完成(t *testing.T) {
	event := baseEvent(EventAdClose)
	event.AdFormat = "interstitial"
	event.AdPlacement = "level_complete"
	event.AdAttemptID = "ad:attempt-1"
	event.AdResult = "completed"
	service := NewService(stubStore{}, time.UTC)
	_, err := service.Ingest(context.Background(), 1, []Event{event})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v", err)
	}
}

func TestOverview广告加载与曝光字段保持稳定(t *testing.T) {
	input := Overview{
		RewardedAdRequests:         12,
		RewardedAdLoads:            15,
		RewardedAdPreloadCreates:   9,
		RewardedAdShows:            10,
		InterstitialAdRequests:     7,
		InterstitialAdLoads:        6,
		InterstitialAdShows:        5,
		InterstitialAdNormalCloses: 4,
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	for field, expected := range map[string]float64{
		"rewardedAdRequests":         12,
		"rewardedAdLoads":            15,
		"rewardedAdPreloadCreates":   9,
		"rewardedAdShows":            10,
		"interstitialAdRequests":     7,
		"interstitialAdLoads":        6,
		"interstitialAdShows":        5,
		"interstitialAdNormalCloses": 4,
	} {
		if output[field] != expected {
			t.Fatalf("%s=%v，期望 %v", field, output[field], expected)
		}
	}
}

func TestOverview进入主线字段保持稳定(t *testing.T) {
	input := Overview{
		CohortMainLevelEnteredPlayers: 180,
		MainLevelEnterRate:            95.74,
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	if output["cohortMainLevelEnteredPlayers"] != float64(180) || output["mainLevelEnterRate"] != 95.74 {
		t.Fatalf("进入主线字段不稳定: %s", payload)
	}
}

func TestOverview留存日期字段保持稳定(t *testing.T) {
	input := Overview{
		RetentionObservationDate: "2026-09-06",
		Day1RetentionCohortDate:  "2026-09-05",
		Day3RetentionCohortDate:  "2026-09-03",
		Day7RetentionCohortDate:  "2026-08-30",
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	for field, expected := range map[string]any{
		"retentionObservationDate": "2026-09-06",
		"day1RetentionCohortDate":  "2026-09-05",
		"day3RetentionCohortDate":  "2026-09-03",
		"day7RetentionCohortDate":  "2026-08-30",
	} {
		if output[field] != expected {
			t.Fatalf("%s=%v，期望 %v", field, output[field], expected)
		}
	}
}

func TestIngest校验道具类型(t *testing.T) {
	event := baseEvent(EventPropUse)
	event.PropType = player.PropTypeRemove
	service := NewService(stubStore{}, time.UTC)
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
}

// BT 继续使用 online_time 秒数求和；15秒主段与广告关闭尾段无需改数据库协议。
func TestBTOnlineTime15秒及尾段兼容(t *testing.T) {
	for _, seconds := range []int{1, 7, 15, 30, 300} {
		event := baseEvent(EventOnlineTime)
		event.DurationSeconds = &seconds
		if err := validateEvent(event); err != nil {
			t.Fatalf("%d秒合法分段被拒绝: %v", seconds, err)
		}
	}
	for _, seconds := range []int{0, -1, 301} {
		event := baseEvent(EventOnlineTime)
		event.DurationSeconds = &seconds
		if err := validateEvent(event); err == nil {
			t.Fatalf("%d秒非法分段未拒绝", seconds)
		}
	}
}

func TestIngest魔药广告与使用独立统计(t *testing.T) {
	service := NewService(stubStore{}, time.UTC)
	for _, name := range []EventName{EventAdRequest, EventAdCreate, EventAdLoad, EventAdShow, EventAdClose, EventAdSuccess, EventAdClaimOK, EventAdFail, EventAdClaimFail} {
		event := baseEvent(name)
		event.AdPlacement = "potion"
		event.AdFormat = "rewarded"
		event.AdAttemptID = "ad:potion"
		if name == EventAdClose {
			event.AdResult = "completed"
		}
		if name == EventAdFail {
			event.AdErrorCode = "NO_FILL"
			event.AdFailureStage = "load"
		}
		if name == EventAdClaimFail {
			event.AdErrorCode = "CLAIM_FAILED"
			event.AdFailureStage = "reward_claim"
		}
		if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	event := baseEvent(EventPropUse)
	event.PropType = "potion"
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); err != nil {
		t.Fatal(err)
	}
	event.PropType = "unknown_prop"
	if _, err := service.Ingest(context.Background(), 1, []Event{event}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("未知道具=%v", err)
	}
}

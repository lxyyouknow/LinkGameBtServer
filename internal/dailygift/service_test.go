package dailygift

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkgame-server/internal/player"
)

type fakeStore struct {
	dayKey    string
	claimDay  string
	offerID   string
	requestID string
}

func (store *fakeStore) GetOrCreate(
	_ context.Context,
	_ uint64,
	dayKey string,
	_ time.Time,
) (Gift, error) {
	store.dayKey = dayKey
	return Gift{OfferID: "dg_20260825_test", State: "available"}, nil
}

func (store *fakeStore) Claim(
	_ context.Context,
	_ uint64,
	dayKey string,
	offerID string,
	requestID string,
	_ time.Time,
) (ClaimResult, error) {
	store.claimDay = dayKey
	store.offerID = offerID
	store.requestID = requestID
	return ClaimResult{Gift: Gift{OfferID: offerID, State: "claimed"}}, nil
}

func TestGetToday使用东京自然日和UTC重置时间(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	service.now = func() time.Time {
		return time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC)
	}

	result, err := service.GetToday(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetToday() error = %v", err)
	}
	if store.dayKey != "2026-08-25" || result.DayKey != "2026-08-25" {
		t.Fatalf("dayKey = %q/%q", store.dayKey, result.DayKey)
	}
	wantReset := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	if !result.NextResetAt.Equal(wantReset) {
		t.Fatalf("nextResetAt = %s，期望 %s", result.NextResetAt, wantReset)
	}
}

func TestGetToday东京零点后切换日期(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	service.now = func() time.Time {
		return time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	}

	if _, err := service.GetToday(context.Background(), 7); err != nil {
		t.Fatalf("GetToday() error = %v", err)
	}
	if store.dayKey != "2026-08-26" {
		t.Fatalf("dayKey = %q，期望 2026-08-26", store.dayKey)
	}
}

func TestClaim拒绝非法幂等参数(t *testing.T) {
	service := NewService(&fakeStore{})
	_, err := service.Claim(context.Background(), 7, "", "bad request id")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Claim() error = %v，期望 ErrInvalidRequest", err)
	}
}

func TestClaim透传当天与幂等参数(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	service.now = func() time.Time {
		return time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	}

	_, err := service.Claim(context.Background(), 7, "dg_20260825_test", "dg-claim:test-1")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if store.claimDay != "2026-08-25" || store.offerID != "dg_20260825_test" || store.requestID != "dg-claim:test-1" {
		t.Fatalf("Claim() 参数错误: %#v", store)
	}
}

func TestClaimResult幂等快照可完整序列化(t *testing.T) {
	themeID := 7
	fragmentIndex := 0
	original := ClaimResult{
		Gift: Gift{
			OfferID: "dg_20260825_test",
			State:   "claimed",
			BaseRewards: []Reward{{
				Type:          "theme_fragment",
				ThemeID:       &themeID,
				FragmentIndex: &fragmentIndex,
				Quantity:      1,
			}},
		},
		Save: player.Save{Revision: 9, ShuffleCount: 3, ThemeFragments: player.DefaultThemeFragments()},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var replay ClaimResult
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if replay.Save.Revision != 9 || replay.Save.ShuffleCount != 3 ||
		len(replay.Gift.BaseRewards) != 1 || replay.Gift.BaseRewards[0].FragmentIndex == nil ||
		*replay.Gift.BaseRewards[0].FragmentIndex != 0 {
		t.Fatalf("幂等快照丢失字段: %#v", replay)
	}
}

func TestGift未领取追加奖励时仍返回bonusClaimedAt(t *testing.T) {
	encoded, err := json.Marshal(Gift{OfferID: "dg_20260829_test", State: "available"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(encoded) == "" || !json.Valid(encoded) {
		t.Fatalf("礼包 JSON 无效: %s", encoded)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	value, exists := payload["bonusClaimedAt"]
	if !exists || value != nil {
		t.Fatalf("bonusClaimedAt = %#v, exists=%v，期望显式 null", value, exists)
	}
}

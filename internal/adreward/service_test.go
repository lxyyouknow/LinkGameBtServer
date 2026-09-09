package adreward

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	placement   Placement
	businessKey string
	sessionID   string
	requestID   string
	adAttemptID string
}

func (store *fakeStore) Create(_ context.Context, _ uint64, placement Placement, businessKey string, _, expiresAt time.Time) (Session, error) {
	store.placement = placement
	store.businessKey = businessKey
	return Session{SessionID: "ad_session_test", Placement: placement, BusinessKey: businessKey, ExpiresAt: expiresAt}, nil
}

func (store *fakeStore) Claim(_ context.Context, _ uint64, sessionID, requestID, adAttemptID string, _ time.Time) (ClaimResult, error) {
	store.sessionID = sessionID
	store.requestID = requestID
	store.adAttemptID = adAttemptID
	return ClaimResult{SessionID: sessionID, Placement: PlacementHint}, nil
}

func TestCreate接受计划书中的广告奖励位(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	result, err := service.Create(context.Background(), 7, PlacementLevelComplete, "level:12")
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID == "" || store.placement != PlacementLevelComplete || store.businessKey != "level:12" {
		t.Fatalf("result/store=%#v/%#v", result, store)
	}
}

func TestCreate接受赛季补签广告位(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	if _, err := service.Create(context.Background(), 7, PlacementSeasonMakeup, "season-makeup:2026-09:1"); err != nil {
		t.Fatal(err)
	}
}

func TestCreate拒绝未定义的奖励位(t *testing.T) {
	service := NewService(&fakeStore{})
	_, err := service.Create(context.Background(), 7, Placement("revive"), "level:1")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
}

func TestClaim要求稳定幂等键(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	if _, err := service.Claim(context.Background(), 7, "ad_session_test", "ad-claim:test", "ad:test-attempt"); err != nil {
		t.Fatal(err)
	}
	if store.sessionID != "ad_session_test" || store.requestID != "ad-claim:test" || store.adAttemptID != "ad:test-attempt" {
		t.Fatalf("store=%#v", store)
	}
}

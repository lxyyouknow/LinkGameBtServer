package dailychallenge

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	dayKey       string
	requestID    string
	attemptID    string
	clearSeconds int
}

func (store *fakeStore) Get(_ context.Context, _ uint64, dayKey string, now time.Time) (State, error) {
	store.dayKey = dayKey
	return State{DayKey: dayKey, ServerTime: now}, nil
}

func (store *fakeStore) Start(_ context.Context, _ uint64, requestID, dayKey string, _ time.Time) (Attempt, error) {
	store.requestID = requestID
	store.dayKey = dayKey
	return Attempt{AttemptID: "dc_test", DayKey: dayKey}, nil
}

func (store *fakeStore) Complete(_ context.Context, _ uint64, attemptID, requestID string, clearSeconds int, dayKey string, _ time.Time) (CompleteResult, error) {
	store.attemptID = attemptID
	store.requestID = requestID
	store.clearSeconds = clearSeconds
	store.dayKey = dayKey
	return CompleteResult{AttemptID: attemptID}, nil
}

func TestService使用东京自然日并转发稳定请求号(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	service.now = func() time.Time { return time.Date(2026, 8, 31, 15, 30, 0, 0, time.UTC) }
	if _, err := service.Start(context.Background(), 7, "daily-start:test"); err != nil {
		t.Fatal(err)
	}
	if store.dayKey != "2026-09-01" || store.requestID != "daily-start:test" {
		t.Fatalf("store=%#v", store)
	}
	if _, err := service.Complete(context.Background(), 7, "dc_test", "daily-complete:test", 86); err != nil {
		t.Fatal(err)
	}
	if store.attemptID != "dc_test" || store.clearSeconds != 86 {
		t.Fatalf("store=%#v", store)
	}
}

func TestService拒绝不合法结算参数(t *testing.T) {
	service := NewService(&fakeStore{})
	for _, test := range []struct {
		attemptID    string
		requestID    string
		clearSeconds int
	}{
		{"", "daily-complete:test", 1},
		{"dc_test", "", 1},
		{"dc_test", "daily-complete:test", -1},
		{"dc_test", "daily-complete:test", 86_401},
	} {
		_, err := service.Complete(context.Background(), 7, test.attemptID, test.requestID, test.clearSeconds)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Complete(%#v) error=%v", test, err)
		}
	}
}

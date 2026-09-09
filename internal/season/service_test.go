package season

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	window Window
	task   Task
}

func (store *fakeStore) Get(_ context.Context, _ uint64, window Window, now time.Time) (State, error) {
	store.window = window
	return State{ServerTime: now}, nil
}

func (store *fakeStore) RecordClear(_ context.Context, _ uint64, _, _ string, _ int, window Window, _ time.Time) (ProgressResult, error) {
	store.window = window
	return ProgressResult{SeasonKey: window.SeasonKey}, nil
}

func (store *fakeStore) Claim(_ context.Context, _ uint64, task Task, _, _, _ string, window Window, _ time.Time) (ClaimResult, error) {
	store.window = window
	store.task = task
	return ClaimResult{AcceptedTask: task}, nil
}

func (store *fakeStore) InspectPlayer(_ context.Context, playerID string, window Window, _ time.Time) (AdminAudit, error) {
	store.window = window
	return AdminAudit{PlayerID: playerID}, nil
}

func TestConfig与原版赛季奖励一致(t *testing.T) {
	configs := AllConfigs()
	if len(configs) != 12 {
		t.Fatalf("赛季配置数量=%d", len(configs))
	}
	for _, config := range configs {
		rewardDays, err := RewardDays(config)
		if err != nil || len(rewardDays) != 25 {
			t.Fatalf("月份 %d 奖励配置=%d err=%v", config.Month, len(rewardDays), err)
		}
		fragments, props := 0, map[string]int64{}
		for _, day := range rewardDays {
			for _, reward := range day.Rewards {
				if reward.Type == "theme_fragment" {
					fragments++
				} else if reward.Type == "prop" {
					props[reward.PropType] += reward.Quantity
				}
			}
		}
		if fragments != 34 || props["hint"] != 15 || props["shuffle"] != 15 || props["remove"] != 15 {
			t.Fatalf("月份 %d 奖励汇总错误: fragments=%d props=%v", config.Month, fragments, props)
		}
	}
}

func TestWindowFor使用东京自然月(t *testing.T) {
	window, err := WindowFor(time.Date(2026, 8, 31, 15, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if window.SeasonKey != "2026-09" || window.DayKey != "2026-09-01" || window.Config.ID != 4 || !window.RewardPeriodOpen {
		t.Fatalf("东京窗口=%#v", window)
	}
	if window.StartsAt != time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC) ||
		window.EndsAt != time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC) {
		t.Fatalf("赛季边界错误: %v - %v", window.StartsAt, window.EndsAt)
	}
}

func TestService拒绝非法幂等键与任务(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	service.now = func() time.Time { return time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC) }
	if _, err := service.RecordLevelClear(context.Background(), 1, "bad id", "main:ok", 3); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("非法通关请求 error=%v", err)
	}
	if _, err := service.Claim(context.Background(), 1, Task("bad"), "season-claim:1", "2026-09", "2026-09-01"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("非法任务 error=%v", err)
	}
	if _, err := service.Claim(context.Background(), 1, TaskLogin, "season-claim:1", "2026-09", "2026-09-01"); err != nil || store.task != TaskLogin {
		t.Fatalf("合法领取 error=%v task=%s", err, store.task)
	}
}

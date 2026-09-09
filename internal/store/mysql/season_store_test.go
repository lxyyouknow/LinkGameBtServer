package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/season"
)

func TestIntegrationSeason通关领取幂等与原子发奖(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-season-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("season-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建赛季测试玩家失败: %v", err)
	}
	window, err := season.WindowFor(now)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSeasonStore(db)
	initial, err := store.Get(ctx, created.ID, window, now)
	if err != nil || initial.SeasonKey != "2026-09" || initial.ConfigID != 4 || initial.Today == nil || !initial.Today.Login.Claimable || len(initial.RewardDays) != 25 {
		t.Fatalf("初始赛季=%#v err=%v", initial, err)
	}
	for index := 0; index < 5; index++ {
		requestID := fmt.Sprintf("season-clear:%d", index)
		completionID := fmt.Sprintf("main:season-%d", index)
		progress, progressErr := store.RecordClear(ctx, created.ID, requestID, completionID, index, window, now.Add(time.Duration(index+1)*time.Second))
		if progressErr != nil || progress.ClearedLevels != index+1 || progress.Claimable != (index == 4) {
			t.Fatalf("第 %d 次通关=%#v err=%v", index+1, progress, progressErr)
		}
		if index == 0 {
			replayed, replayErr := store.RecordClear(ctx, created.ID, requestID, completionID, index, window, now.Add(1500*time.Millisecond))
			if replayErr != nil || replayed.ClearedLevels != 1 {
				t.Fatalf("重复通关没有幂等=%#v err=%v", replayed, replayErr)
			}
		}
	}
	login, err := store.Claim(ctx, created.ID, season.TaskLogin, "season-claim:login", window.SeasonKey, window.DayKey, window, now.Add(10*time.Second))
	if err != nil || len(login.GrantedRewards) != 0 || login.Save.Revision != 1 || login.Season.LoginClaimedAt == nil {
		t.Fatalf("登录任务领取=%#v err=%v", login, err)
	}
	loginReplay, err := store.Claim(ctx, created.ID, season.TaskLogin, "season-claim:login", window.SeasonKey, window.DayKey, window, now.Add(11*time.Second))
	if err != nil || !reflect.DeepEqual(loginReplay, login) {
		t.Fatalf("登录任务幂等重放不一致=%#v err=%v", loginReplay, err)
	}
	clear, err := store.Claim(ctx, created.ID, season.TaskClearLevels, "season-claim:clear", window.SeasonKey, window.DayKey, window, now.Add(12*time.Second))
	if err != nil || len(clear.GrantedRewards) != 5 || clear.Save.Revision != 2 || clear.Save.HintCount != 3 || clear.Save.ShuffleCount != 3 || clear.Save.RemoveCount != 3 || !reflect.DeepEqual(clear.Season.ClaimedDays, []int{5}) {
		t.Fatalf("通关任务原子发奖=%#v err=%v", clear, err)
	}
	clearReplay, err := store.Claim(ctx, created.ID, season.TaskClearLevels, "season-claim:clear", window.SeasonKey, window.DayKey, window, now.Add(13*time.Second))
	if err != nil || !reflect.DeepEqual(clearReplay, clear) {
		t.Fatalf("通关任务幂等重放不一致=%#v err=%v", clearReplay, err)
	}
	if _, err := store.Claim(ctx, created.ID, season.TaskClearLevels, "season-claim:other", window.SeasonKey, window.DayKey, window, now.Add(14*time.Second)); !errors.Is(err, season.ErrDayAlreadyRewarded) {
		t.Fatalf("换请求号重复领取 error=%v", err)
	}
	final, err := store.Get(ctx, created.ID, window, now.Add(15*time.Second))
	if err != nil || final.Today == nil || final.Today.RewardClaimedAt == nil || !reflect.DeepEqual(final.ClaimedDays, []int{5}) {
		t.Fatalf("最终赛季状态=%#v err=%v", final, err)
	}
	audit, err := store.InspectPlayer(ctx, created.PublicID, window, now.Add(16*time.Second))
	if err != nil || len(audit.ClearEvents) != 5 || len(audit.Claims) != 2 || len(audit.RewardMutations) != 5 ||
		audit.Claims[1].Result.Save.Revision != 2 {
		t.Fatalf("赛季 GM 审计=%#v err=%v", audit, err)
	}
}

func TestIntegrationSeason两项任务并发领取只发奖一次(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-season-concurrent-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("season-concurrent-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建赛季并发测试玩家失败: %v", err)
	}
	window, err := season.WindowFor(now)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSeasonStore(db)
	if _, err := store.Get(ctx, created.ID, window, now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < season.ClearLevelTarget; index++ {
		if _, err := store.RecordClear(
			ctx,
			created.ID,
			fmt.Sprintf("season-concurrent-clear:%d", index),
			fmt.Sprintf("main:season-concurrent-%d", index),
			index,
			window,
			now.Add(time.Duration(index+1)*time.Second),
		); err != nil {
			t.Fatalf("准备第 %d 次通关失败: %v", index+1, err)
		}
	}

	type claimOutcome struct {
		result season.ClaimResult
		err    error
	}
	outcomes := make(chan claimOutcome, 2)
	claim := func(task season.Task, requestID string) {
		result, claimErr := store.Claim(
			ctx, created.ID, task, requestID, window.SeasonKey, window.DayKey, window, now.Add(10*time.Second),
		)
		outcomes <- claimOutcome{result: result, err: claimErr}
	}
	go claim(season.TaskLogin, "season-concurrent-claim:login")
	go claim(season.TaskClearLevels, "season-concurrent-claim:clear")

	grantedResponses := 0
	for index := 0; index < 2; index++ {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("并发领取失败: %v", outcome.err)
		}
		if len(outcome.result.GrantedRewards) > 0 {
			grantedResponses++
		}
	}
	if grantedResponses != 1 {
		t.Fatalf("最终发奖响应数量=%d，期望 1", grantedResponses)
	}
	final, err := store.Get(ctx, created.ID, window, now.Add(11*time.Second))
	if err != nil || final.Today == nil || final.Today.RewardClaimedAt == nil ||
		final.Today.Login.ClaimedAt == nil || final.Today.ClearLevels.ClaimedAt == nil {
		t.Fatalf("并发领取最终状态=%#v err=%v", final, err)
	}
	save, err := NewSaveStore(db).GetSave(ctx, created.ID)
	if err != nil || save.Revision != 2 || save.HintCount != 3 || save.ShuffleCount != 3 || save.RemoveCount != 3 {
		t.Fatalf("并发领取最终存档=%#v err=%v", save, err)
	}
}

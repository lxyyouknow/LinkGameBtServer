package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/dailychallenge"
)

func TestIntegrationDailyChallenge免费广告与重复结算闭环(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-daily-challenge-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("daily-challenge-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建每日挑战测试玩家失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE player_saves SET level=10 WHERE player_id=?`, created.ID); err != nil {
		t.Fatalf("准备每日挑战解锁进度失败: %v", err)
	}

	store := NewDailyChallengeStore(db)
	dayKey := dailychallenge.TokyoDayKey(now)
	state, err := store.Get(ctx, created.ID, dayKey, now)
	if err != nil || !state.Unlocked || state.CompletedToday || state.ChallengeLevel != 0 {
		t.Fatalf("初始状态=%#v err=%v", state, err)
	}
	first, err := store.Start(ctx, created.ID, "daily-start:free", dayKey, now.Add(time.Second))
	if err != nil || first.Mode != "free" || first.ChallengeIndex != 0 || len(first.Rewards) < 1 || len(first.Rewards) > 3 {
		t.Fatalf("免费轮=%#v err=%v", first, err)
	}
	resumed, err := store.Start(ctx, created.ID, "daily-start:free-other", dayKey, now.Add(2*time.Second))
	if err != nil || resumed.AttemptID != first.AttemptID || !reflect.DeepEqual(resumed.Rewards, first.Rewards) {
		t.Fatalf("失败重试没有恢复同一轮次: first=%#v resumed=%#v err=%v", first, resumed, err)
	}
	completed, err := store.Complete(ctx, created.ID, first.AttemptID, "daily-complete:free", 86, dayKey, now.Add(3*time.Second))
	if err != nil || completed.Save.Revision != 2 || completed.Challenge.ChallengeLevel != 1 || completed.Challenge.CompletionCount != 1 {
		t.Fatalf("免费轮结算=%#v err=%v", completed, err)
	}
	replayedComplete, err := store.Complete(ctx, created.ID, first.AttemptID, "daily-complete:other", 99, dayKey, now.Add(4*time.Second))
	if err != nil || replayedComplete.Save.Revision != completed.Save.Revision {
		t.Fatalf("完成轮次幂等重放=%#v err=%v", replayedComplete, err)
	}
	if _, err := store.Start(ctx, created.ID, "daily-start:no-ad", dayKey, now.Add(5*time.Second)); !errors.Is(err, dailychallenge.ErrReplayRequired) {
		t.Fatalf("无广告资格开始 error=%v", err)
	}

	adStore := NewAdRewardStore(db)
	businessKey := fmt.Sprintf("daily-challenge:%s:%d", dayKey, completed.Challenge.ChallengeLevel)
	session, err := adStore.Create(ctx, created.ID, adreward.PlacementDailyChallengeReplay, businessKey, now.Add(6*time.Second), now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("创建再次挑战广告会话失败: %v", err)
	}
	claim, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:daily-challenge", "", now.Add(7*time.Second))
	if err != nil || claim.Challenge == nil || !claim.Challenge.ReplayAvailable || len(claim.GrantedRewards) != 0 || claim.Save.Revision != 2 {
		t.Fatalf("核销再次挑战资格=%#v err=%v", claim, err)
	}
	replayedClaim, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:daily-challenge", "", now.Add(7500*time.Millisecond))
	if err != nil || replayedClaim.Challenge == nil || !replayedClaim.Challenge.ReplayAvailable || replayedClaim.Save.Revision != claim.Save.Revision {
		t.Fatalf("重复核销没有重放首次资格结果=%#v err=%v", replayedClaim, err)
	}
	replayAttempt, err := store.Start(ctx, created.ID, "daily-start:replay", dayKey, now.Add(8*time.Second))
	if err != nil || replayAttempt.Mode != "replay" || replayAttempt.ChallengeLevel != 1 {
		t.Fatalf("广告轮=%#v err=%v", replayAttempt, err)
	}
	whileActive, err := store.Get(ctx, created.ID, dayKey, now.Add(9*time.Second))
	if err != nil || !whileActive.ReplayAvailable || whileActive.ActiveAttempt == nil || whileActive.ActiveAttempt.AttemptID != replayAttempt.AttemptID {
		t.Fatalf("退出后资格/轮次未保留=%#v err=%v", whileActive, err)
	}
	replayCompleted, err := store.Complete(ctx, created.ID, replayAttempt.AttemptID, "daily-complete:replay", 75, dayKey, now.Add(10*time.Second))
	if err != nil || replayCompleted.Save.Revision != 3 || replayCompleted.Challenge.ReplayAvailable || replayCompleted.Challenge.CompletionCount != 2 {
		t.Fatalf("广告轮结算=%#v err=%v", replayCompleted, err)
	}
}

func TestIntegrationDailyChallenge奖励边界与跨日重置(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-daily-challenge-edge-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("daily-challenge-edge-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE player_saves SET level=10 WHERE player_id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	for _, themeID := range dailyChallengeThemeIDs {
		for fragmentIndex := 0; fragmentIndex < 34; fragmentIndex++ {
			if themeID == 7 && fragmentIndex >= 32 {
				continue
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO player_theme_fragments (player_id,theme_id,fragment_index,acquired_at) VALUES (?,?,?,?)`, created.ID, themeID, fragmentIndex, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	store := NewDailyChallengeStore(db)
	dayKey := dailychallenge.TokyoDayKey(now)
	attempt, err := store.Start(ctx, created.ID, "daily-start:edge", dayKey, now)
	if err != nil || len(attempt.Rewards) != 2 || attempt.Rewards[0].Type != "theme_fragment" || *attempt.Rewards[0].ThemeID != 7 {
		t.Fatalf("剩余两块奖励=%#v err=%v", attempt.Rewards, err)
	}
	if _, err := store.Complete(ctx, created.ID, attempt.AttemptID, "daily-complete:edge", 30, dayKey, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// 新东京自然日恢复免费轮，但绝对 challengeLevel 继续前进。
	tomorrow := now.Add(24 * time.Hour)
	next, err := store.Get(ctx, created.ID, dailychallenge.TokyoDayKey(tomorrow), tomorrow)
	if err != nil || next.CompletedToday || next.ReplayAvailable || next.CompletionCount != 0 || next.ChallengeLevel != 1 {
		t.Fatalf("跨日状态=%#v err=%v", next, err)
	}
	coinAttempt, err := store.Start(ctx, created.ID, "daily-start:all-complete", next.DayKey, tomorrow.Add(time.Second))
	if err != nil || len(coinAttempt.Rewards) != 1 || coinAttempt.Rewards[0].Type != "coins" || coinAttempt.Rewards[0].Quantity != 120 {
		t.Fatalf("四套主题集齐后的星星奖励=%#v err=%v", coinAttempt.Rewards, err)
	}
}

func TestIntegrationDailyChallenge未完成轮次跨日过期后可重新开始(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 8, 31, 14, 59, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-daily-challenge-expire-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("daily-challenge-expire-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(48 * time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE player_saves SET level=10 WHERE player_id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	store := NewDailyChallengeStore(db)
	oldDay := dailychallenge.TokyoDayKey(now)
	oldAttempt, err := store.Start(ctx, created.ID, "daily-start:before-midnight", oldDay, now)
	if err != nil {
		t.Fatal(err)
	}
	afterMidnight := now.Add(2 * time.Minute)
	newDay := dailychallenge.TokyoDayKey(afterMidnight)
	newAttempt, err := store.Start(ctx, created.ID, "daily-start:after-midnight", newDay, afterMidnight)
	if err != nil || newAttempt.AttemptID == oldAttempt.AttemptID || newAttempt.ChallengeLevel != oldAttempt.ChallengeLevel {
		t.Fatalf("跨日重新开始失败: old=%#v new=%#v err=%v", oldAttempt, newAttempt, err)
	}
	if _, err := store.Complete(ctx, created.ID, oldAttempt.AttemptID, "daily-complete:expired", 30, newDay, afterMidnight.Add(time.Second)); !errors.Is(err, dailychallenge.ErrExpired) {
		t.Fatalf("旧轮次跨日后仍可结算: %v", err)
	}
}

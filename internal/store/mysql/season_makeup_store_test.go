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
	"linkgame-server/internal/season"
)

func TestIntegrationSeasonMakeup原子核销恢复与审计(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-season-makeup-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("season-makeup-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建补签测试玩家失败: %v", err)
	}
	window, err := season.WindowFor(now)
	if err != nil {
		t.Fatal(err)
	}
	seasonStore := NewSeasonStore(db)
	initial, err := seasonStore.Get(ctx, created.ID, window, now)
	if err != nil || initial.MakeupRemaining != season.MaxMakeupCount || len(initial.ClaimedDays) != 0 {
		t.Fatalf("初始补签状态=%#v err=%v", initial, err)
	}
	adStore := NewAdRewardStore(db)
	session, err := adStore.Create(ctx, created.ID, adreward.PlacementSeasonMakeup, "season-makeup:2026-09:1", now, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("创建补签广告会话失败: %v", err)
	}
	if _, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:season-missing-attempt", "", now.Add(time.Second)); !errors.Is(err, adreward.ErrInvalidRequest) {
		t.Fatalf("缺少 adAttemptId error=%v", err)
	}
	claim, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:season", "ad:season-attempt", now.Add(2*time.Second))
	if err != nil || claim.Season == nil || claim.Season.MakeupRemaining != 4 || !reflect.DeepEqual(claim.Season.ClaimedDays, []int{1}) ||
		len(claim.GrantedRewards) == 0 || claim.Save.Revision != 2 {
		t.Fatalf("补签核销=%#v err=%v", claim, err)
	}
	replay, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:season", "ad:season-attempt", now.Add(3*time.Second))
	if err != nil || !reflect.DeepEqual(replay, claim) {
		t.Fatalf("补签幂等重放=%#v err=%v", replay, err)
	}
	if _, err := adStore.Claim(ctx, created.ID, session.SessionID, "ad-claim:season-other", "ad:season-attempt", now.Add(4*time.Second)); !errors.Is(err, adreward.ErrAlreadyClaimed) {
		t.Fatalf("更换请求号重复核销 error=%v", err)
	}
	audit, err := seasonStore.InspectPlayer(ctx, created.PublicID, window, now.Add(5*time.Second))
	if err != nil || len(audit.MakeupClaims) != 1 || audit.MakeupClaims[0].SessionID != session.SessionID ||
		audit.MakeupClaims[0].AdAttemptID != "ad:season-attempt" || audit.MakeupClaims[0].SaveRevision != 2 {
		t.Fatalf("补签 GM 审计=%#v err=%v", audit, err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE player_season_months SET makeup_remaining=1,makeup_recovery_day_key='2026-09-02'
		WHERE player_id=? AND season_key='2026-09'`, created.ID); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	laterWindow, err := season.WindowFor(later)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := seasonStore.Get(ctx, created.ID, laterWindow, later)
	if err != nil || recovered.MakeupRemaining != 2 {
		t.Fatalf("跨多日只恢复一次=%#v err=%v", recovered, err)
	}
	recoveredAgain, err := seasonStore.Get(ctx, created.ID, laterWindow, later.Add(time.Minute))
	if err != nil || recoveredAgain.MakeupRemaining != 2 {
		t.Fatalf("同一东京日不得重复恢复=%#v err=%v", recoveredAgain, err)
	}
}

func TestIntegrationSeasonMakeup拒绝当日未来已领与零次数(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	providerUID := fmt.Sprintf("integration-season-makeup-reject-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID) })
	tokenHash := sha256.Sum256([]byte("season-makeup-reject-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(time.Now()),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	window, _ := season.WindowFor(now)
	if _, err := NewSeasonStore(db).Get(ctx, created.ID, window, now); err != nil {
		t.Fatal(err)
	}
	store := NewAdRewardStore(db)
	for _, key := range []string{"season-makeup:2026-09:3", "season-makeup:2026-09:4"} {
		if _, err := store.Create(ctx, created.ID, adreward.PlacementSeasonMakeup, key, now, now.Add(time.Minute)); !errors.Is(err, season.ErrMakeupDayNotEligible) {
			t.Fatalf("%s error=%v", key, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE player_season_months SET makeup_remaining=0 WHERE player_id=? AND season_key='2026-09'`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, created.ID, adreward.PlacementSeasonMakeup, "season-makeup:2026-09:1", now, now.Add(time.Minute)); !errors.Is(err, season.ErrMakeupLimitReached) {
		t.Fatalf("零次数 error=%v", err)
	}
}

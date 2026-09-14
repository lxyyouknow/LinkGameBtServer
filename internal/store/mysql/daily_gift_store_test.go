package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/dailygift"
	"linkgame-server/internal/player"
)

func TestLimitedThemeRewardCandidates只返回未拥有的限定碎片(t *testing.T) {
	owned := map[[2]int]struct{}{
		{7, 0}: {},
		{1, 0}: {},
	}
	candidates := limitedThemeRewardCandidates(owned)
	wantTotal := 17*player.LimitedThemeFragmentCount - 1
	if len(candidates) != wantTotal {
		t.Fatalf("候选数 = %d，期望 %d", len(candidates), wantTotal)
	}
	for _, candidate := range candidates {
		if candidate.ThemeID == nil || candidate.FragmentIndex == nil || !player.IsLimitedTheme(*candidate.ThemeID) {
			t.Fatalf("返回了普通主题: %#v", candidate)
		}
		if *candidate.ThemeID == 7 && *candidate.FragmentIndex == 0 {
			t.Fatalf("返回了已拥有碎片: %#v", candidate)
		}
	}
}

func TestDailyGiftMutationID不超过数据库上限(t *testing.T) {
	offerID := "dg_20260825_0123456789abcdef0123456789abcdef"
	for _, suffix := range []string{"shuffle", "theme"} {
		identifier := giftMutationID(offerID, suffix)
		if len(identifier) > 64 {
			t.Fatalf("mutation ID 长度 = %d: %s", len(identifier), identifier)
		}
	}
}

func TestIntegrationDailyGift领取与幂等重放(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("integration-daily-gift-%d", now.UnixNano())
	t.Cleanup(func() {
		cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID)
	})
	tokenHash := sha256.Sum256([]byte("daily-gift-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider:    "test_account",
		ProviderUID: providerUID,
		PublicID:    testPublicID(now),
		TokenHash:   tokenHash,
		ExpiresAt:   now.Add(time.Hour),
		Now:         now,
	})
	if err != nil {
		t.Fatalf("创建每日礼包集成测试玩家失败: %v", err)
	}

	store := NewDailyGiftStore(db)
	dayKey := tokyoDayKey(now)
	first, err := store.GetOrCreate(ctx, created.ID, dayKey, now)
	if err != nil {
		t.Fatalf("首次查询每日礼包失败: %v", err)
	}
	second, err := store.GetOrCreate(ctx, created.ID, dayKey, now.Add(time.Second))
	if err != nil {
		t.Fatalf("重复查询每日礼包失败: %v", err)
	}
	if first.OfferID == "" || first.OfferID != second.OfferID || first.State != "available" {
		t.Fatalf("重复查询没有返回同一礼包: first=%#v second=%#v", first, second)
	}
	if len(first.BaseRewards) != 2 || len(first.BonusRewards) != 2 ||
		first.BaseRewards[0].PropType != player.PropTypeShuffle ||
		first.BonusRewards[0].PropType != player.PropTypeShuffle ||
		*first.BaseRewards[1].ThemeID == *first.BonusRewards[1].ThemeID &&
			*first.BaseRewards[1].FragmentIndex == *first.BonusRewards[1].FragmentIndex {
		t.Fatalf("每日礼包两阶段奖励不符合契约: %#v", first)
	}

	requestID := "dg-claim:integration-test"
	result, err := store.Claim(ctx, created.ID, dayKey, first.OfferID, requestID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("免费领取每日礼包失败: %v", err)
	}
	if result.Gift.State != "claimed" || result.Save.Coins != 0 || result.Save.ShuffleCount != 2 || result.Save.Revision != 2 ||
		len(result.GrantedRewards) != 2 {
		t.Fatalf("每日礼包基础领取结果错误: %#v", result)
	}
	replay, err := store.Claim(ctx, created.ID, dayKey, first.OfferID, requestID, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("基础领取幂等重放失败: %v", err)
	}
	if replay.Save.Revision != result.Save.Revision || replay.Save.ShuffleCount != result.Save.ShuffleCount {
		t.Fatalf("基础领取幂等重放改变了结果: first=%#v replay=%#v", result, replay)
	}
	_, err = store.Claim(ctx, created.ID, dayKey, first.OfferID, "dg-claim:other", now.Add(4*time.Second))
	if !errors.Is(err, dailygift.ErrAlreadyClaimed) {
		t.Fatalf("不同 requestId 重复领取 error = %v，期望 ErrAlreadyClaimed", err)
	}

	adStore := NewAdRewardStore(db)
	adSession, err := adStore.Create(ctx, created.ID, adreward.PlacementDailyGift, first.OfferID, now.Add(5*time.Second), now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("创建每日礼包广告会话失败: %v", err)
	}
	bonus, err := adStore.Claim(ctx, created.ID, adSession.SessionID, "ad-claim:daily-gift", "", now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("核销每日礼包广告会话失败: %v", err)
	}
	if bonus.Save.Revision != 3 || bonus.Save.ShuffleCount != 3 || len(bonus.GrantedRewards) != 2 {
		t.Fatalf("每日礼包追加领取结果错误: %#v", bonus)
	}

	var giftCount, propMutationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_daily_gifts WHERE player_id = ?`, created.ID).Scan(&giftCount); err != nil {
		t.Fatalf("查询礼包记录失败: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_prop_mutations WHERE player_id=? AND reason IN (?,?)`,
		created.ID, dailyGiftBaseReason, dailyGiftBonusReason).Scan(&propMutationCount); err != nil {
		t.Fatalf("查询礼包道具流水失败: %v", err)
	}
	if giftCount != 1 || propMutationCount != 2 {
		t.Fatalf("礼包/道具流水数量 = %d/%d，期望 1/2", giftCount, propMutationCount)
	}
}

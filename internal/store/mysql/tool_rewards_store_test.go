package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/analytics"
	"linkgame-server/internal/auth"
)

// 真 MySQL 验证事务、唯一键和超时后重放，避免仅测试奖励常量。
func TestIntegrationToolRewards魔杖魔药核销与隔离(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	uid := fmt.Sprintf("integration-tools-%d", now.UnixNano())
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", uid) })
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: uid, PublicID: testPublicID(now), TokenHash: sha256.Sum256([]byte(uid)), ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := NewSaveStore(db).GetSave(ctx, created.ID)
	if err != nil || baseline.HintCount != 1 || baseline.ShuffleCount != 1 || baseline.RemoveCount != 1 {
		t.Fatalf("新号库存=%+v err=%v", baseline, err)
	}
	store := NewAdRewardStore(db)
	create := func(placement adreward.Placement, key string) adreward.Session {
		t.Helper()
		session, err := store.Create(ctx, created.ID, placement, key, now, now.Add(10*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	potion := create(adreward.PlacementPotion, "potion:first")
	if _, err := store.Claim(ctx, created.ID, potion.SessionID, "claim:potion", "", now); !errors.Is(err, adreward.ErrInvalidRequest) {
		t.Fatalf("缺少 attempt=%v", err)
	}
	if _, err := store.Claim(ctx, created.ID+99999, potion.SessionID, "claim:potion", "ad:potion", now); !errors.Is(err, adreward.ErrNotFound) {
		t.Fatalf("跨玩家核销=%v", err)
	}
	var wg sync.WaitGroup
	results := make(chan adreward.ClaimResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := store.Claim(ctx, created.ID, potion.SessionID, "claim:potion", "ad:potion", now)
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.Placement != adreward.PlacementPotion || result.GrantedRewards == nil || len(result.GrantedRewards) != 0 || !reflect.DeepEqual(result.Save, baseline) {
			t.Fatalf("魔药不应改变存档=%+v", result)
		}
		encoded, _ := json.Marshal(result)
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &raw)
		if string(raw["grantedRewards"]) != "[]" {
			t.Fatalf("空奖励必须序列化为数组=%s", raw["grantedRewards"])
		}
	}
	// 已核销会话过期后同 ID 重放仍成功，模拟响应丢失后的重试。
	if _, err := store.Claim(ctx, created.ID, potion.SessionID, "claim:potion", "ad:potion", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, created.ID, potion.SessionID, "claim:other", "ad:potion", now); !errors.Is(err, adreward.ErrAlreadyClaimed) {
		t.Fatalf("换 request=%v", err)
	}
	if _, err := store.Claim(ctx, created.ID, potion.SessionID, "claim:potion", "ad:other", now); !errors.Is(err, adreward.ErrIdempotencyKeyReused) {
		t.Fatalf("换 attempt=%v", err)
	}
	next := create(adreward.PlacementPotion, "potion:next")
	if _, err := store.Claim(ctx, created.ID, next.SessionID, "claim:next", "ad:potion", now); !errors.Is(err, adreward.ErrIdempotencyKeyReused) {
		t.Fatalf("跨会话 attempt=%v", err)
	}
	if _, err := store.Claim(ctx, created.ID, next.SessionID, "claim:potion", "ad:next", now); !errors.Is(err, adreward.ErrIdempotencyKeyReused) {
		t.Fatalf("跨会话 request=%v", err)
	}
	expired := create(adreward.PlacementPotion, "potion:expired")
	if _, err := store.Claim(ctx, created.ID, expired.SessionID, "claim:expired", "ad:expired", now.Add(time.Hour)); !errors.Is(err, adreward.ErrExpired) {
		t.Fatalf("过期核销=%v", err)
	}
	hint := create(adreward.PlacementHint, "hint:first")
	for i := 0; i < 2; i++ {
		r, err := store.Claim(ctx, created.ID, hint.SessionID, "claim:hint", "ad:hint", now)
		if err != nil || len(r.GrantedRewards) != 1 || r.GrantedRewards[0].Quantity != 1 || r.Save.HintCount != 2 || r.Save.Revision != baseline.Revision+1 {
			t.Fatalf("魔杖应只增加1=%+v err=%v", r, err)
		}
	}
	// 不同会话并发复用 attempt：唯一键冲突必须回滚其中一笔资产更新。
	a := create(adreward.PlacementHint, "hint:race-a")
	b := create(adreward.PlacementHint, "hint:race-b")
	raceErrs := make(chan error, 2)
	for i, s := range []adreward.Session{a, b} {
		wg.Add(1)
		go func(i int, s adreward.Session) {
			defer wg.Done()
			_, err := store.Claim(ctx, created.ID, s.SessionID, fmt.Sprintf("claim:race-%d", i), "ad:race", now)
			raceErrs <- err
		}(i, s)
	}
	wg.Wait()
	close(raceErrs)
	successes := 0
	for err := range raceErrs {
		if err == nil {
			successes++
		} else if !errors.Is(err, adreward.ErrIdempotencyKeyReused) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("并发成功次数=%d", successes)
	}
	final, err := NewSaveStore(db).GetSave(ctx, created.ID)
	if err != nil || final.HintCount != 3 || final.ShuffleCount != 1 || final.RemoveCount != 1 || final.Revision != baseline.Revision+2 {
		t.Fatalf("最终库存=%+v err=%v", final, err)
	}
	// 真实写入验证数据库 prop_type 约束，并核对 GM 按 potion 独立分组。
	analyticsStore := NewAnalyticsStore(db)
	events := []analytics.Event{
		{ID: "tool-use", Name: analytics.EventPropUse, PropType: "potion", EventTime: now.Format(time.RFC3339), AppID: 1, SDKType: 1000, GameSessionID: "tools-session"},
		{ID: "potion-claim", Name: analytics.EventAdClaimOK, AdPlacement: "potion", AdFormat: "rewarded", AdAttemptID: "ad:potion", EventTime: now.Format(time.RFC3339), AppID: 1, SDKType: 1000, GameSessionID: "tools-session"},
	}
	if _, err := analytics.NewService(analyticsStore, time.UTC).Ingest(ctx, created.ID, events); err != nil {
		t.Fatalf("魔药统计入库=%v", err)
	}
	if _, err := analytics.NewService(analyticsStore, time.UTC).Ingest(ctx, created.ID, events); err != nil {
		t.Fatal(err)
	}
	sdk := 1000
	filter := analytics.Filter{AppID: 1, SDKType: &sdk, From: now.Add(-time.Hour), To: now.Add(24 * time.Hour)}
	props, err := analyticsStore.PropUsage(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, prop := range props {
		if prop.Key == "potion" && prop.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("GM魔药去重分组=%+v", props)
	}
	var mutations int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_prop_mutations WHERE player_id=?", created.ID).Scan(&mutations); err != nil || mutations != 2 {
		t.Fatalf("只应有两笔魔杖流水=%d err=%v", mutations, err)
	}
}

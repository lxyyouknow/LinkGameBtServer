package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"linkgame-server/internal/adreward"
	"linkgame-server/internal/analytics"
	"linkgame-server/internal/auth"
	"linkgame-server/internal/player"
	"sync"
	"testing"
	"time"
)

func TestIntegrationProgressChestRewardsAndAdStatistics(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	uid := fmt.Sprintf("chest-%d", now.UnixNano())
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{Provider: "test_account", ProviderUID: uid, PublicID: testPublicID(now), TokenHash: sha256.Sum256([]byte(uid)), ExpiresAt: now.Add(time.Hour), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", uid) })
	saves := player.NewService(NewSaveStore(db))
	input := player.UpdateSaveInput{Revision: 1, Level: 1, SelectedTheme: 0, CollectingTheme: 1, ClientVersion: "chest-test", SoundEnabled: true, MusicEnabled: true, EffectsEnabled: true, VibrationEnabled: true}
	for _, milestone := range []int{25, 50, 75} {
		input.PropMutations = append(input.PropMutations, player.PropMutation{ID: fmt.Sprintf("prop:first:%d", milestone), PropType: player.PropTypeRemove, Delta: 1, Reason: player.PropReasonGiftReward, CreatedAt: now.Format(time.RFC3339Nano)})
	}
	first, err := saves.UpdateSave(ctx, created.ID, input)
	if err != nil || first.RemoveCount != 4 || len(first.AcceptedPropMutationIDs) != 3 {
		t.Fatalf("three chests: %+v %v", first, err)
	}
	if _, err = saves.UpdateSave(ctx, created.ID, input); !errors.Is(err, player.ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	input.Revision = first.Revision
	replay, err := saves.UpdateSave(ctx, created.ID, input)
	if err != nil || replay.RemoveCount != 4 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	input.Revision = replay.Revision
	for i := range input.PropMutations {
		input.PropMutations[i].ID = fmt.Sprintf("prop:restart:%d", i)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := saves.UpdateSave(ctx, created.ID, input); out <- err }()
	}
	wg.Wait()
	close(out)
	successes := 0
	for err := range out {
		if err == nil {
			successes++
		} else if !errors.Is(err, player.ErrRevisionConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent winners=%d", successes)
	}
	base, err := saves.GetSave(ctx, created.ID)
	if err != nil || base.RemoveCount != 7 {
		t.Fatalf("restart base: %+v %v", base, err)
	}
	ads := adreward.NewService(NewAdRewardStore(db))
	// 未完播只创建 session，基础奖励保持；不同重开会话允许分别领取。
	if _, err = ads.Create(ctx, created.ID, adreward.PlacementAutoRemove, "progress-chest:75:abandoned"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		session, err := ads.Create(ctx, created.ID, adreward.PlacementAutoRemove, fmt.Sprintf("progress-chest:25:run%d", i))
		if err != nil {
			t.Fatal(err)
		}
		result, err := ads.Claim(ctx, created.ID, session.SessionID, fmt.Sprintf("request:%d", i), fmt.Sprintf("attempt:%d", i))
		if err != nil {
			t.Fatal(err)
		}
		again, err := ads.Claim(ctx, created.ID, session.SessionID, fmt.Sprintf("request:%d", i), fmt.Sprintf("attempt:%d", i))
		if err != nil || again.Save.RemoveCount != result.Save.RemoveCount {
			t.Fatalf("claim replay: %v", err)
		}
		if result.Save.RemoveCount != int64(8+i) || len(result.GrantedRewards) != 1 || result.GrantedRewards[0].Quantity != 1 {
			t.Fatalf("claim must add only one: %+v", result)
		}
	}
	stats := NewAnalyticsStore(db)
	events := []analytics.Event{}
	version := fmt.Sprintf("chest-%d", now.UnixNano())
	for _, placement := range []string{"auto_remove", "potion"} {
		for _, name := range []analytics.EventName{analytics.EventAdRequest, analytics.EventAdShow, analytics.EventAdSuccess, analytics.EventAdClaimOK} {
			events = append(events, analytics.Event{ID: placement + ":" + string(name), Name: name, EventTime: now.Format(time.RFC3339Nano), AppID: 1, SDKType: 1000, Channel: 0, ClientVersion: version, Platform: "tiktok", GameSessionID: "chest-session", AdFormat: "rewarded", AdPlacement: placement, AdAttemptID: "attempt-" + placement})
		}
	}
	for i := 0; i < 2; i++ {
		if _, err = stats.Ingest(ctx, created.ID, events, now, now.In(time.FixedZone("JST", 9*3600)).Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := stats.AdPerformance(ctx, analytics.Filter{From: now.Add(-time.Hour), To: now.Add(time.Hour), AppID: 1, Channel: 0})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, r := range rows {
		if r.ClientVersion == version {
			found++
			if r.Requests != 1 || r.Shows != 1 || r.Successes != 1 || r.RewardClaims != 1 {
				t.Fatalf("ad lifecycle counts: %+v", r)
			}
		}
	}
	if found != 2 {
		t.Fatalf("missing potion/chest ad groups: %d", found)
	}
}

func TestIntegrationEffectiveLoginPlayersByNewPlayerCohortAndTokyoDate(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	channel := int(now.UnixNano()%1000000) + 2000000
	loc := time.FixedZone("JST", 9*3600)
	from := time.Date(2026, 9, 10, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 2)
	sdk := 1000
	stats := NewAnalyticsStore(db)
	for i := 0; i < 9; i++ {
		uid := fmt.Sprintf("effective-%d-%d", now.UnixNano(), i)
		created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{Provider: "test_account", ProviderUID: uid, PublicID: testPublicID(now.Add(time.Duration(i) * time.Microsecond)), TokenHash: sha256.Sum256([]byte(uid)), ExpiresAt: now.Add(time.Hour), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", uid) })
		level := 1
		kind := analytics.EventLevelStart
		eventSDK := 1000
		eventChannel := channel
		at := from.Add(23*time.Hour + 59*time.Minute)
		switch i {
		case 0:
			level = 0
		case 2:
			level = 2
		case 3:
			eventSDK = 0
		case 4:
			eventChannel++
		case 5:
			at = to
		case 6:
			kind = analytics.EventNextLevel
		}
		if i == 7 || i == 8 {
			// 老玩家当天重玩不计；第二天新增、第三天才进入第二关仍归属新增日。
			firstAt := from.Add(-time.Minute)
			if i == 8 {
				firstAt = from.Add(24*time.Hour + time.Minute)
				at = to.Add(time.Hour)
			}
			firstLevel := 0
			first := analytics.Event{ID: "first-entry", Name: analytics.EventLevelStart, EventTime: firstAt.Format(time.RFC3339Nano), AppID: 1, SDKType: 1000, Channel: channel, GameSessionID: "new-player-session", Level: &firstLevel}
			if _, err := stats.Ingest(ctx, created.ID, []analytics.Event{first}, firstAt.UTC(), firstAt.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
		event := analytics.Event{ID: "effective-event", Name: kind, EventTime: at.Format(time.RFC3339Nano), AppID: 1, SDKType: eventSDK, Channel: eventChannel, GameSessionID: "effective-session", Level: &level}
		for retry := 0; retry < 2; retry++ {
			if _, err = stats.Ingest(ctx, created.ID, []analytics.Event{event}, at.UTC(), at.Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 {
			event.ID = "second-day"
			if _, err = stats.Ingest(ctx, created.ID, []analytics.Event{event}, at.Add(2*time.Minute).UTC(), at.Add(2*time.Minute).Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
	}
	filter := analytics.Filter{From: from, To: to, AppID: 1, SDKType: &sdk, Channel: channel, ReportUTCOffsetMinutes: 540}
	overview, err := stats.Overview(ctx, filter)
	if err != nil || overview.EffectiveLoginPlayers != 2 {
		t.Fatalf("range dedup/filter: %+v %v", overview, err)
	}
	daily, err := stats.Daily(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 2 || daily[0].Date != "2026-09-10" || daily[1].Date != "2026-09-11" || daily[0].EffectiveLoginPlayers != 1 || daily[1].EffectiveLoginPlayers != 1 {
		t.Fatalf("Tokyo daily=%+v", daily)
	}
	for _, day := range daily {
		if day.EffectiveLoginPlayers > day.NewPlayers {
			t.Fatalf("有效登录不能超过当天新增: %+v", day)
		}
	}
	oneDay := filter
	oneDay.From = from.Add(24 * time.Hour)
	oneDayResult, err := stats.Overview(ctx, oneDay)
	if err != nil || oneDayResult.EffectiveLoginPlayers != 1 || oneDayResult.NewPlayers != 1 {
		t.Fatalf("新增日归属（含后续第二关、排除老玩家重玩）: %+v %v", oneDayResult, err)
	}
	filter.SDKType = nil
	overview, err = stats.Overview(ctx, filter)
	if err != nil || overview.EffectiveLoginPlayers != 3 {
		t.Fatalf("all platforms: %d %v", overview.EffectiveLoginPlayers, err)
	}
}

package mysql

import (
	"context"
	"crypto/sha256"
	"fmt"
	"linkgame-server/internal/analytics"
	"linkgame-server/internal/auth"
	"testing"
	"time"
)

func TestIntegrationSharedAdConsumptionAttribution(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	uid := fmt.Sprintf("shared-ad-%d", now.UnixNano())
	user, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{Provider: "test_account", ProviderUID: uid, PublicID: testPublicID(now), TokenHash: sha256.Sum256([]byte(uid)), ExpiresAt: now.Add(time.Hour), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupAuthIntegrationIdentity(t, db, "test_account", uid) })
	stats := NewAnalyticsStore(db)
	warmAt := now.Add(-24 * time.Hour)
	preloaded := true
	emit := func(id string, name analytics.EventName, placement, attempt string, at time.Time, channel int) {
		e := analytics.Event{ID: id, Name: name, EventTime: at.Format(time.RFC3339Nano), AppID: 1, SDKType: 1000, Channel: channel, ClientVersion: uid, Platform: "tiktok", GameSessionID: "shared", AdFormat: "rewarded", AdPlacement: placement, AdAttemptID: attempt, AdPreloaded: &preloaded}
		if name == analytics.EventAdFail {
			e.AdErrorCode = "50101"
			e.AdFailureStage = "load"
		}
		if _, err := stats.Ingest(ctx, user.ID, []analytics.Event{e}, at, at.Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	emit("warm", analytics.EventAdCreate, "potion", "shared", warmAt, 0)
	emit("load", analytics.EventAdLoad, "potion", "shared", warmAt, 0)
	emit("fail", analytics.EventAdFail, "potion", "shared", warmAt, 0)
	emit("request", analytics.EventAdRequest, "auto_remove", "shared", now, 0)
	emit("show", analytics.EventAdShow, "auto_remove", "shared", now, 0)
	// 同 attempt 的另一渠道不能串入共享缓存归因；尚未消费保留来源。
	emit("other", analytics.EventAdCreate, "hint", "shared", warmAt, 1)
	emit("unused", analytics.EventAdCreate, "shuffle", "unused", warmAt, 0)
	filter := analytics.Filter{From: warmAt.Add(-time.Hour), To: warmAt.Add(time.Hour), AppID: 1, Channel: 0}
	rows, err := stats.AdPerformance(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ClientVersion != uid {
			continue
		}
		if r.Placement == "potion" {
			t.Fatalf("消费后仍归预热来源: %+v", r)
		}
		if r.Placement == "auto_remove" {
			found = true
			if r.Creates != 1 || r.Loads != 1 || r.Requests != 0 || r.Failures != 1 {
				t.Fatalf("跨日计数: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("缺失实际消费位")
	}
	failures, err := stats.AdFailures(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Placement != "auto_remove" {
		t.Fatalf("失败归因: %+v", failures)
	}
	filter.Channel = 1
	rows, err = stats.AdPerformance(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Placement != "hint" {
		t.Fatalf("渠道隔离: %+v", rows)
	}
}

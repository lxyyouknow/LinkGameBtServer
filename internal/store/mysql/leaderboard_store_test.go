package mysql

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/leaderboard"
	"linkgame-server/internal/platform"
	"linkgame-server/internal/player"
)

func TestIntegrationGlobalLeaderboard稳定排序资料与榜外Self(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := time.Now().UTC().Truncate(time.Microsecond)
	type seededPlayer struct {
		id    uint64
		level int
		at    time.Time
	}
	players := make([]seededPlayer, 0, 4)
	for index, item := range []struct {
		level    int
		offset   time.Duration
		provider string
	}{{10, time.Minute, "tiktok"}, {10, 2 * time.Minute, "test_account"}, {5, 3 * time.Minute, "test_account"}, {1, 4 * time.Minute, "test_account"}} {
		providerUID := fmt.Sprintf("leaderboard-%d-%d", base.UnixNano(), index)
		tokenHash := sha256.Sum256([]byte("leaderboard-token-" + providerUID))
		created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
			Provider: item.provider, ProviderUID: providerUID,
			PublicID:  testPublicID(base.Add(time.Duration(index+1) * time.Microsecond)),
			TokenHash: tokenHash, ExpiresAt: base.Add(time.Hour), Now: base,
		})
		if err != nil {
			t.Fatalf("创建排行榜测试玩家失败: %v", err)
		}
		t.Cleanup(func() { cleanupAuthIntegrationPlayer(t, db, created.ID) })
		reachedAt := base.Add(item.offset)
		if _, err := db.ExecContext(ctx, `UPDATE player_saves SET level=?,reached_level_at=? WHERE player_id=?`, item.level, reachedAt, created.ID); err != nil {
			t.Fatal(err)
		}
		players = append(players, seededPlayer{id: created.ID, level: item.level, at: reachedAt})
	}

	store := NewLeaderboardStore(db)
	profile, err := store.UpdateTikTokProfile(ctx, players[0].id, platform.AuthorizedProfile{
		Provider: "tiktok", ProviderUID: fmt.Sprintf("leaderboard-%d-0", base.UnixNano()),
		DisplayName: "PairMaster", AvatarURL: "https://example.com/avatar.png",
	}, base.Add(5*time.Minute))
	if err != nil || profile.DisplayName != "PairMaster" {
		t.Fatalf("保存授权资料失败: profile=%#v err=%v", profile, err)
	}

	entries, self, err := store.Global(ctx, players[3].id, 2)
	if err != nil {
		t.Fatalf("读取总排行榜失败: %v", err)
	}
	if len(entries) != 2 || entries[0].PlayerNumber == entries[1].PlayerNumber {
		t.Fatalf("Top 2 返回错误: %#v", entries)
	}
	if entries[0].DisplayName != "PairMaster" || entries[0].Level != 11 || entries[0].Rank == nil || *entries[0].Rank != 1 {
		t.Fatalf("同关卡更早到达者未稳定排第一: %#v", entries[0])
	}
	if self.Rank == nil || *self.Rank != 4 || !self.IsSelf || self.Level != 2 {
		t.Fatalf("榜外当前玩家返回错误: %#v", self)
	}
	if self.DisplayName != leaderboard.DefaultDisplayName(self.PlayerNumber) || self.AvatarURL != "" {
		t.Fatalf("未授权默认资料错误: %#v", self)
	}
}

func TestIntegrationSaveOnlyOnLevelAdvanceChangesReachedTime(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("leaderboard-reached-%d", now.UnixNano())
	tokenHash := sha256.Sum256([]byte("leaderboard-reached-token-" + providerUID))
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(now),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupAuthIntegrationPlayer(t, db, created.ID) })
	var initial time.Time
	if err := db.QueryRowContext(ctx, `SELECT reached_level_at FROM player_saves WHERE player_id=?`, created.ID).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	service := player.NewService(NewSaveStore(db))
	input := player.UpdateSaveInput{Revision: 1, Level: 0, SelectedTheme: 0, CollectingTheme: 1, ClientVersion: "leaderboard-test"}
	if _, err := service.UpdateSave(ctx, created.ID, input); err != nil {
		t.Fatal(err)
	}
	var unchanged time.Time
	if err := db.QueryRowContext(ctx, `SELECT reached_level_at FROM player_saves WHERE player_id=?`, created.ID).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged.Equal(initial) {
		t.Fatalf("普通同步改变首次到达时间: before=%v after=%v", initial, unchanged)
	}
	time.Sleep(time.Millisecond)
	input.Revision = 2
	input.Level = 1
	if _, err := service.UpdateSave(ctx, created.ID, input); err != nil {
		t.Fatal(err)
	}
	var advanced time.Time
	if err := db.QueryRowContext(ctx, `SELECT reached_level_at FROM player_saves WHERE player_id=?`, created.ID).Scan(&advanced); err != nil {
		t.Fatal(err)
	}
	if !advanced.After(initial) {
		t.Fatalf("升关未刷新首次到达时间: before=%v after=%v", initial, advanced)
	}
}

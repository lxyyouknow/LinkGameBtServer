package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"linkgame-server/internal/auth"
	"linkgame-server/internal/player"
)

type recordingExecer struct {
	query string
	args  []any
}

func (execer *recordingExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	execer.query = query
	execer.args = args
	return staticResult(1), nil
}

type staticResult int64

func (result staticResult) LastInsertId() (int64, error) { return 0, nil }
func (result staticResult) RowsAffected() (int64, error) { return int64(result), nil }

func TestEnsureSave使用零初始库存且不覆盖旧库存(t *testing.T) {
	execer := &recordingExecer{}
	if err := ensureSave(context.Background(), execer, 42); err != nil {
		t.Fatalf("ensureSave() error = %v", err)
	}

	wantArgs := []any{uint64(42), int64(0), int64(0), int64(0)}
	if len(execer.args) != len(wantArgs) {
		t.Fatalf("参数数量 = %d，期望 %d", len(execer.args), len(wantArgs))
	}
	for index := range wantArgs {
		if execer.args[index] != wantArgs[index] {
			t.Fatalf("参数 %d = %#v，期望 %#v", index, execer.args[index], wantArgs[index])
		}
	}
	if !strings.Contains(execer.query, "hint_count, shuffle_count, remove_count") {
		t.Fatalf("建档 SQL 未显式写入三类初始库存: %s", execer.query)
	}
	duplicateClause := strings.SplitN(execer.query, "ON DUPLICATE KEY UPDATE", 2)
	if len(duplicateClause) != 2 || strings.Contains(duplicateClause[1], "_count") {
		t.Fatalf("重复登录可能覆盖已有库存: %s", execer.query)
	}
}

func TestIntegrationTikTok平台任务奖励原子入账且跨设备幂等(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("integration-tiktok-missions-%d", now.UnixNano())
	t.Cleanup(func() {
		cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID)
	})
	tokenHash := sha256.Sum256([]byte("tiktok-mission-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(now),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建平台任务测试玩家失败: %v", err)
	}

	service := player.NewService(NewSaveStore(db))
	createdAt := now.Format(time.RFC3339Nano)
	input := player.UpdateSaveInput{
		Revision: 1, Level: 0, SelectedTheme: 0, CollectingTheme: 1,
		SoundEnabled: true, MusicEnabled: true, EffectsEnabled: true, VibrationEnabled: true,
		ClientVersion: "mission-test",
		PropMutations: []player.PropMutation{
			{ID: player.HomeShortcutHintMutationID, PropType: player.PropTypeHint, Delta: 3, Reason: player.PropReasonGiftReward, CreatedAt: createdAt},
			{ID: player.ProfileRemoveMutationID, PropType: player.PropTypeRemove, Delta: 3, Reason: player.PropReasonGiftReward, CreatedAt: createdAt},
		},
		ThemeFragmentMutations: []player.ThemeFragmentMutation{
			{ID: player.HomeShortcutThemeMutationID, ThemeID: 7, FragmentIndex: 0, Reason: player.ThemeReasonLevelComplete, CreatedAt: createdAt},
			{ID: player.ProfileThemeMutationID, ThemeID: 13, FragmentIndex: 1, Reason: player.ThemeReasonLevelComplete, CreatedAt: createdAt},
		},
	}
	first, err := service.UpdateSave(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("首次领取平台任务奖励失败: %v", err)
	}
	if first.Revision != 2 || first.HintCount != 3 || first.RemoveCount != 3 ||
		len(first.ClaimedTikTokMissions) != 2 || len(first.AcceptedPropMutationIDs) != 2 ||
		len(first.AcceptedThemeFragmentMutationIDs) != 2 {
		t.Fatalf("首次平台任务奖励结果错误: %#v", first)
	}

	input.Revision = first.Revision
	replay, err := service.UpdateSave(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("重复领取平台任务奖励失败: %v", err)
	}
	if replay.HintCount != 3 || replay.RemoveCount != 3 || replay.Revision != 3 ||
		len(replay.ClaimedTikTokMissions) != 2 {
		t.Fatalf("重复领取改变了权威奖励: %#v", replay)
	}

	var missionCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_tiktok_mission_claims WHERE player_id=?`, created.ID).Scan(&missionCount); err != nil {
		t.Fatalf("查询平台任务领取记录失败: %v", err)
	}
	if missionCount != 2 {
		t.Fatalf("平台任务领取记录 = %d，期望 2", missionCount)
	}
}

func TestIntegration旧版平台任务流水保持兼容(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("integration-legacy-tiktok-mission-%d", now.UnixNano())
	t.Cleanup(func() {
		cleanupAuthIntegrationIdentity(t, db, "test_account", providerUID)
	})
	tokenHash := sha256.Sum256([]byte("legacy-tiktok-mission-token-" + providerUID))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	created, err := NewAuthStore(db).LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider: "test_account", ProviderUID: providerUID, PublicID: testPublicID(now),
		TokenHash: tokenHash, ExpiresAt: now.Add(time.Hour), Now: now,
	})
	if err != nil {
		t.Fatalf("创建旧版平台任务测试玩家失败: %v", err)
	}

	createdAt := now.Format(time.RFC3339Nano)
	result, err := player.NewService(NewSaveStore(db)).UpdateSave(ctx, created.ID, player.UpdateSaveInput{
		Revision: 1, Level: 0, SelectedTheme: 0, CollectingTheme: 1,
		SoundEnabled: true, MusicEnabled: true, EffectsEnabled: true, VibrationEnabled: true,
		ClientVersion: "legacy-mission-test",
		CoinMutations: []player.CoinMutation{{
			ID: player.HomeShortcutLegacyCoinID, Delta: 300, Reason: player.CoinReasonGiftReward, CreatedAt: createdAt,
		}},
		PropMutations: []player.PropMutation{
			{ID: player.HomeShortcutLegacyHintID, PropType: player.PropTypeHint, Delta: 1, Reason: player.PropReasonGiftReward, CreatedAt: createdAt},
			{ID: player.HomeShortcutLegacyShuffleID, PropType: player.PropTypeShuffle, Delta: 1, Reason: player.PropReasonGiftReward, CreatedAt: createdAt},
		},
	})
	if err != nil {
		t.Fatalf("旧版添加桌面奖励失败: %v", err)
	}
	if result.Coins != 300 || result.HintCount != 1 || result.ShuffleCount != 1 ||
		len(result.ClaimedTikTokMissions) != 1 || result.ClaimedTikTokMissions[0] != player.TikTokMissionHomeShortcut {
		t.Fatalf("旧版添加桌面奖励结果错误: %#v", result)
	}
}

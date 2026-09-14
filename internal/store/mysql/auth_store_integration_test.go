package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"linkgame-server/internal/auth"
)

func TestIntegrationAuthStoreLoginIdentity(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	store := NewAuthStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("integration-login-%d", now.UnixNano())
	t.Cleanup(func() {
		cleanupAuthIntegrationIdentity(
			t,
			db,
			"test_account",
			providerUID,
		)
	})
	firstHash := sha256.Sum256([]byte("first-login-token-" + providerUID))
	secondHash := sha256.Sum256([]byte("second-login-token-" + providerUID))
	thirdHash := sha256.Sum256([]byte("third-login-token-" + providerUID))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	firstPlayer, err := store.LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider:    "test_account",
		ProviderUID: providerUID,
		PublicID:    testPublicID(now),
		TokenHash:   firstHash,
		ExpiresAt:   now.Add(time.Hour),
		Now:         now,
	})
	if err != nil {
		t.Fatalf("首次测试账号登录失败: %v", err)
	}

	secondPlayer, err := store.LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider:    "test_account",
		ProviderUID: providerUID,
		PublicID:    testPublicID(now.Add(time.Microsecond)),
		TokenHash:   secondHash,
		ExpiresAt:   now.Add(2 * time.Hour),
		Now:         now.Add(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("重复测试账号登录失败: %v", err)
	}
	if secondPlayer != firstPlayer {
		t.Fatalf(
			"相同测试账号返回不同玩家: 首次=%#v，再次=%#v",
			firstPlayer,
			secondPlayer,
		)
	}
	assertIntegrationPropCounts(t, ctx, db, firstPlayer.ID, 1, 1, 1)

	if _, err := db.ExecContext(ctx, `UPDATE player_saves SET hint_count = 4, shuffle_count = 5, remove_count = 6 WHERE player_id = ?`, firstPlayer.ID); err != nil {
		t.Fatalf("准备旧玩家库存失败: %v", err)
	}
	thirdPlayer, err := store.LoginIdentity(ctx, auth.IdentityLoginParams{
		Provider:    "test_account",
		ProviderUID: providerUID,
		PublicID:    testPublicID(now.Add(2 * time.Microsecond)),
		TokenHash:   thirdHash,
		ExpiresAt:   now.Add(3 * time.Hour),
		Now:         now.Add(2 * time.Microsecond),
	})
	if err != nil {
		t.Fatalf("旧账号再次登录失败: %v", err)
	}
	if thirdPlayer != firstPlayer {
		t.Fatalf("旧账号返回不同玩家: %#v，期望 %#v", thirdPlayer, firstPlayer)
	}
	assertIntegrationPropCounts(t, ctx, db, firstPlayer.ID, 4, 5, 6)
	var playerNumber uint64
	if err := db.QueryRowContext(ctx, `SELECT player_number FROM player_public_profiles WHERE player_id=?`, firstPlayer.ID).Scan(&playerNumber); err != nil || playerNumber == 0 {
		t.Fatalf("排行榜公开编号未稳定创建: number=%d err=%v", playerNumber, err)
	}

	var identityCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM player_identities
		 WHERE provider = ? AND provider_uid = ?`,
		"test_account",
		providerUID,
	).Scan(&identityCount); err != nil {
		t.Fatalf("查询测试账号身份数量失败: %v", err)
	}
	if identityCount != 1 {
		t.Fatalf("测试账号身份数量 = %d，期望 1", identityCount)
	}

	var sessionCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM player_sessions
		 WHERE player_id = ?
		   AND token_hash IN (?, ?, ?)`,
		firstPlayer.ID,
		firstHash[:],
		secondHash[:],
		thirdHash[:],
	).Scan(&sessionCount); err != nil {
		t.Fatalf("查询测试账号会话数量失败: %v", err)
	}
	if sessionCount != 3 {
		t.Fatalf("数据库中的 Token 哈希数量 = %d，期望 3", sessionCount)
	}
}

func TestIntegrationAuthStoreConcurrentIdentityCreation(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	store := NewAuthStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	providerUID := fmt.Sprintf("integration-concurrent-%d", now.UnixNano())
	t.Cleanup(func() {
		cleanupAuthIntegrationIdentity(
			t,
			db,
			"test_account",
			providerUID,
		)
	})

	const loginCount = 8
	players := make([]auth.Player, loginCount)
	errs := make([]error, loginCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(loginCount)
	for index := 0; index < loginCount; index++ {
		go func(index int) {
			defer waitGroup.Done()
			loginTime := now.Add(time.Duration(index) * time.Microsecond)
			tokenHash := sha256.Sum256(
				[]byte(fmt.Sprintf("concurrent-token-%d-%s", index, providerUID)),
			)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			players[index], errs[index] = store.LoginIdentity(
				ctx,
				auth.IdentityLoginParams{
					Provider:    "test_account",
					ProviderUID: providerUID,
					PublicID:    testPublicID(loginTime),
					TokenHash:   tokenHash,
					ExpiresAt:   loginTime.Add(time.Hour),
					Now:         loginTime,
				},
			)
		}(index)
	}
	waitGroup.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发登录失败: %v", index, err)
		}
	}
	firstPlayer := players[0]
	for index, player := range players[1:] {
		if player != firstPlayer {
			t.Fatalf(
				"第 %d 个并发登录返回不同玩家: %#v，期望 %#v",
				index+1,
				player,
				firstPlayer,
			)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var identityCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM player_identities
		 WHERE provider = ? AND provider_uid = ?`,
		"test_account",
		providerUID,
	).Scan(&identityCount); err != nil {
		t.Fatalf("查询并发测试身份数量失败: %v", err)
	}
	if identityCount != 1 {
		t.Fatalf("并发创建后的身份数量 = %d，期望 1", identityCount)
	}
	var saveCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_saves WHERE player_id = ?`, firstPlayer.ID).Scan(&saveCount); err != nil {
		t.Fatalf("查询并发创建后的存档数量失败: %v", err)
	}
	if saveCount != 1 {
		t.Fatalf("并发创建后的存档数量 = %d，期望 1", saveCount)
	}
	var publicProfileCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_public_profiles WHERE player_id=?`, firstPlayer.ID).Scan(&publicProfileCount); err != nil || publicProfileCount != 1 {
		t.Fatalf("并发创建后的公开编号数量=%d err=%v，期望 1", publicProfileCount, err)
	}
	assertIntegrationPropCounts(t, ctx, db, firstPlayer.ID, 1, 1, 1)
}

func assertIntegrationPropCounts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	playerID uint64,
	wantHint int64,
	wantShuffle int64,
	wantRemove int64,
) {
	t.Helper()
	var hint, shuffle, remove int64
	if err := db.QueryRowContext(ctx, `SELECT hint_count, shuffle_count, remove_count FROM player_saves WHERE player_id = ?`, playerID).
		Scan(&hint, &shuffle, &remove); err != nil {
		t.Fatalf("查询玩家道具库存失败: %v", err)
	}
	if hint != wantHint || shuffle != wantShuffle || remove != wantRemove {
		t.Fatalf("道具库存 = %d/%d/%d，期望 %d/%d/%d", hint, shuffle, remove, wantHint, wantShuffle, wantRemove)
	}
}

func TestIntegrationAuthStoreSessionStates(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	publicID := testPublicID(now)
	result, err := db.ExecContext(
		ctx,
		`INSERT INTO players (
			public_id,
			status,
			created_at,
			updated_at,
			last_login_at
		) VALUES (?, 'active', ?, ?, ?)`,
		publicID,
		now,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("创建集成测试玩家失败: %v", err)
	}
	playerID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取集成测试玩家 ID 失败: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cleanupCancel()
		if _, err := db.ExecContext(
			cleanupContext,
			"DELETE FROM player_sessions WHERE player_id = ?",
			playerID,
		); err != nil {
			t.Errorf("清理集成测试会话失败: %v", err)
		}
		if _, err := db.ExecContext(
			cleanupContext,
			"DELETE FROM players WHERE id = ?",
			playerID,
		); err != nil {
			t.Errorf("清理集成测试玩家失败: %v", err)
		}
	})

	validHash := sha256.Sum256([]byte("valid-token-123456789012345678901"))
	expiredHash := sha256.Sum256([]byte("expired-token-1234567890123456789"))
	revokedHash := sha256.Sum256([]byte("revoked-token-1234567890123456789"))
	insertTestSession(
		t,
		ctx,
		db,
		playerID,
		validHash,
		now.Add(time.Hour),
		nil,
		now,
	)
	insertTestSession(
		t,
		ctx,
		db,
		playerID,
		expiredHash,
		now.Add(-time.Second),
		nil,
		now,
	)
	insertTestSession(
		t,
		ctx,
		db,
		playerID,
		revokedHash,
		now.Add(time.Hour),
		&now,
		now,
	)

	store := NewAuthStore(db)

	player, err := store.AuthenticateSession(ctx, validHash, now)
	if err != nil {
		t.Fatalf("有效会话认证失败: %v", err)
	}
	if player.ID != uint64(playerID) || player.PublicID != publicID {
		t.Fatalf("有效会话返回错误玩家: %#v", player)
	}

	invalidHash := sha256.Sum256([]byte("invalid-token-123456789012345678"))
	tests := []struct {
		name string
		hash [sha256.Size]byte
	}{
		{name: "无效会话", hash: invalidHash},
		{name: "过期会话", hash: expiredHash},
		{name: "已撤销会话", hash: revokedHash},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.AuthenticateSession(ctx, test.hash, now)
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf(
					"AuthenticateSession() 错误 = %v，期望 ErrUnauthenticated",
					err,
				)
			}
		})
	}
}

func openAuthIntegrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 MYSQL_TEST_DSN，跳过本地 MySQL 集成测试")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("创建测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	return db
}

func cleanupAuthIntegrationPlayer(t *testing.T, db *sql.DB, playerID uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, statement := range []string{
		"DELETE FROM player_season_makeup_claims WHERE player_id = ?",
		"DELETE FROM player_season_task_claims WHERE player_id = ?",
		"DELETE FROM player_season_clear_events WHERE player_id = ?",
		"DELETE FROM player_season_days WHERE player_id = ?",
		"DELETE FROM player_season_months WHERE player_id = ?",
		"DELETE FROM analytics_events WHERE player_id = ?",
		"DELETE FROM analytics_funnel_events WHERE player_id = ?",
		"DELETE FROM analytics_daily_user_stats WHERE player_id = ?",
		"DELETE FROM analytics_player_stats WHERE player_id = ?",
		"DELETE FROM player_ad_sessions WHERE player_id = ?",
		"DELETE FROM daily_challenge_attempts WHERE player_id = ?",
		"DELETE FROM player_daily_challenge_states WHERE player_id = ?",
		"DELETE FROM player_daily_gifts WHERE player_id = ?",
		"DELETE FROM player_tiktok_mission_claims WHERE player_id = ?",
		"DELETE FROM player_public_profiles WHERE player_id = ?",
		"DELETE FROM player_theme_mutations WHERE player_id = ?",
		"DELETE FROM player_theme_fragments WHERE player_id = ?",
		"DELETE FROM player_prop_mutations WHERE player_id = ?",
		"DELETE FROM player_coin_mutations WHERE player_id = ?",
		"DELETE FROM player_sessions WHERE player_id = ?",
		"DELETE FROM player_saves WHERE player_id = ?",
		"DELETE FROM player_identities WHERE player_id = ?",
		"DELETE FROM players WHERE id = ?",
	} {
		if _, err := db.ExecContext(ctx, statement, playerID); err != nil {
			t.Errorf("清理认证集成测试数据失败: %v", err)
		}
	}
}

func cleanupAuthIntegrationIdentity(
	t *testing.T,
	db *sql.DB,
	provider string,
	providerUID string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var playerID uint64
	err := db.QueryRowContext(
		ctx,
		`SELECT player_id
		 FROM player_identities
		 WHERE provider = ? AND provider_uid = ?`,
		provider,
		providerUID,
	).Scan(&playerID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		t.Errorf("查询待清理的认证集成测试玩家失败: %v", err)
		return
	}
	cleanupAuthIntegrationPlayer(t, db, playerID)
}

func insertTestSession(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	playerID int64,
	tokenHash [sha256.Size]byte,
	expiresAt time.Time,
	revokedAt *time.Time,
	now time.Time,
) {
	t.Helper()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO player_sessions (
			player_id,
			token_hash,
			expires_at,
			created_at,
			last_used_at,
			revoked_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		playerID,
		tokenHash[:],
		expiresAt,
		now,
		now,
		revokedAt,
	); err != nil {
		t.Fatalf("创建集成测试会话失败: %v", err)
	}
}

func testPublicID(now time.Time) string {
	hash := sha256.Sum256([]byte(now.Format(time.RFC3339Nano)))
	encoded := hex.EncodeToString(hash[:16])
	return encoded[0:8] + "-" +
		encoded[8:12] + "-" +
		encoded[12:16] + "-" +
		encoded[16:20] + "-" +
		encoded[20:32]
}

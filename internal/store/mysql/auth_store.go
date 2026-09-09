package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"linkgame-server/internal/auth"
)

var errConcurrentIdentityCreation = errors.New("玩家身份被并发创建")

// AuthStore 使用 MySQL 保存游客、测试账号等身份和数据库会话。
type AuthStore struct {
	db *sql.DB
}

// NewAuthStore 创建认证数据存储。
func NewAuthStore(db *sql.DB) *AuthStore {
	return &AuthStore{db: db}
}

// LoginIdentity 在短事务内查找或创建玩家身份，并写入新会话。
func (store *AuthStore) LoginIdentity(
	ctx context.Context,
	params auth.IdentityLoginParams,
) (auth.Player, error) {
	for attempt := 0; attempt < 2; attempt++ {
		player, err := store.loginIdentityAttempt(ctx, params)
		if !errors.Is(err, errConcurrentIdentityCreation) {
			return player, err
		}
	}
	return auth.Player{}, fmt.Errorf("并发创建玩家身份失败，请重试")
}

func (store *AuthStore) loginIdentityAttempt(
	ctx context.Context,
	params auth.IdentityLoginParams,
) (auth.Player, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return auth.Player{}, fmt.Errorf("开始身份登录事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	player, status, err := findIdentityPlayerForUpdate(
		ctx,
		tx,
		params.Provider,
		params.ProviderUID,
	)
	switch {
	case err == nil:
		if status != "active" {
			return auth.Player{}, auth.ErrAccountDisabled
		}
	case errors.Is(err, sql.ErrNoRows):
		player, err = createIdentityPlayer(ctx, tx, params)
		if errors.Is(err, errConcurrentIdentityCreation) {
			return auth.Player{}, err
		}
		if err != nil {
			return auth.Player{}, err
		}
	default:
		return auth.Player{}, fmt.Errorf("查询玩家身份失败: %w", err)
	}

	// 游客、测试账号和平台账号共用该幂等建档入口；已有库存不会被覆盖。
	if err := ensureSave(ctx, tx, player.ID); err != nil {
		return auth.Player{}, err
	}
	if err := ensurePlayerPublicProfile(ctx, tx, player.ID); err != nil {
		return auth.Player{}, err
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE players
		 SET last_login_at = ?, updated_at = ?
		 WHERE id = ?`,
		params.Now,
		params.Now,
		player.ID,
	); err != nil {
		return auth.Player{}, fmt.Errorf("更新玩家登录时间失败: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO player_sessions (
			player_id,
			token_hash,
			expires_at,
			created_at,
			last_used_at
		) VALUES (?, ?, ?, ?, ?)`,
		player.ID,
		params.TokenHash[:],
		params.ExpiresAt,
		params.Now,
		params.Now,
	); err != nil {
		return auth.Player{}, fmt.Errorf("创建玩家会话失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return auth.Player{}, fmt.Errorf("提交身份登录事务失败: %w", err)
	}
	return player, nil
}

func findIdentityPlayerForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	provider string,
	providerUID string,
) (auth.Player, string, error) {
	var player auth.Player
	var status string
	err := tx.QueryRowContext(
		ctx,
		`SELECT p.id, p.public_id, p.status
		 FROM player_identities AS i
		 INNER JOIN players AS p ON p.id = i.player_id
		 WHERE i.provider = ? AND i.provider_uid = ?
		 FOR UPDATE`,
		provider,
		providerUID,
	).Scan(&player.ID, &player.PublicID, &status)
	return player, status, err
}

func createIdentityPlayer(
	ctx context.Context,
	tx *sql.Tx,
	params auth.IdentityLoginParams,
) (auth.Player, error) {
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO players (
			public_id,
			status,
			created_at,
			updated_at,
			last_login_at
		) VALUES (?, ?, ?, ?, ?)`,
		params.PublicID,
		"active",
		params.Now,
		params.Now,
		params.Now,
	)
	if err != nil {
		return auth.Player{}, fmt.Errorf("创建玩家失败: %w", err)
	}

	playerID, err := result.LastInsertId()
	if err != nil {
		return auth.Player{}, fmt.Errorf("读取新玩家 ID 失败: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO player_identities (
			player_id,
			provider,
			provider_uid,
			created_at
		) VALUES (?, ?, ?, ?)`,
		playerID,
		params.Provider,
		params.ProviderUID,
		params.Now,
	); err != nil {
		if isIdentityDuplicate(err) {
			return auth.Player{}, errConcurrentIdentityCreation
		}
		return auth.Player{}, fmt.Errorf("创建玩家身份失败: %w", err)
	}

	return auth.Player{
		ID:       uint64(playerID),
		PublicID: params.PublicID,
	}, nil
}

func isIdentityDuplicate(err error) bool {
	var mysqlError *drivermysql.MySQLError
	return errors.As(err, &mysqlError) &&
		mysqlError.Number == 1062 &&
		strings.Contains(
			mysqlError.Message,
			"uk_player_identities_provider_uid",
		)
}

// AuthenticateSession 验证 Token 哈希、有效期、撤销状态和玩家状态。
func (store *AuthStore) AuthenticateSession(
	ctx context.Context,
	tokenHash [sha256.Size]byte,
	now time.Time,
) (auth.Player, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return auth.Player{}, fmt.Errorf("开始会话认证事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var sessionID uint64
	var player auth.Player
	err = tx.QueryRowContext(
		ctx,
		`SELECT s.id, p.id, p.public_id
		 FROM player_sessions AS s
		 INNER JOIN players AS p ON p.id = s.player_id
		 WHERE s.token_hash = ?
		   AND s.revoked_at IS NULL
		   AND s.expires_at > ?
		   AND p.status = ?
		 FOR UPDATE`,
		tokenHash[:],
		now,
		"active",
	).Scan(&sessionID, &player.ID, &player.PublicID)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Player{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Player{}, fmt.Errorf("查询玩家会话失败: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE player_sessions
		 SET last_used_at = ?
		 WHERE id = ?`,
		now,
		sessionID,
	); err != nil {
		return auth.Player{}, fmt.Errorf("更新会话使用时间失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return auth.Player{}, fmt.Errorf("提交会话认证事务失败: %w", err)
	}

	return player, nil
}

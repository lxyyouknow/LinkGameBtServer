package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"linkgame-server/internal/leaderboard"
	"linkgame-server/internal/platform"
)

const maxJSONSafePlayerNumber uint64 = 9_007_199_254_740_991

type LeaderboardStore struct{ db *sql.DB }

func NewLeaderboardStore(db *sql.DB) *LeaderboardStore { return &LeaderboardStore{db: db} }

func ensurePlayerPublicProfile(ctx context.Context, execer execContexter, playerID uint64) error {
	if _, err := execer.ExecContext(ctx, `INSERT IGNORE INTO player_public_profiles (player_id) VALUES (?)`, playerID); err != nil {
		return fmt.Errorf("创建玩家公开编号失败: %w", err)
	}
	return nil
}

func (store *LeaderboardStore) Global(ctx context.Context, playerID uint64, limit int) ([]leaderboard.Entry, leaderboard.Entry, error) {
	if err := ensurePlayerPublicProfile(ctx, store.db, playerID); err != nil {
		return nil, leaderboard.Entry{}, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, leaderboard.Entry{}, fmt.Errorf("开始排行榜快照失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const ranked = `WITH ranked AS (
		SELECT ROW_NUMBER() OVER (ORDER BY s.level DESC, COALESCE(s.reached_level_at, s.updated_at) ASC, p.id ASC) AS rank_number,
			pp.player_number, pp.authorization_status, pp.display_name, pp.avatar_url,
			s.level + 1 AS display_level, p.id AS player_id
		FROM player_saves AS s
		INNER JOIN players AS p ON p.id = s.player_id AND p.status = 'active'
		INNER JOIN player_public_profiles AS pp ON pp.player_id = p.id
	)`
	rows, err := tx.QueryContext(ctx, ranked+`
		SELECT rank_number, player_number, authorization_status, display_name, avatar_url, display_level, player_id
		FROM ranked ORDER BY rank_number LIMIT ?`, limit)
	if err != nil {
		return nil, leaderboard.Entry{}, fmt.Errorf("查询总排行榜失败: %w", err)
	}
	entries := make([]leaderboard.Entry, 0, limit)
	for rows.Next() {
		entry, _, err := scanLeaderboardEntry(rows, playerID)
		if err != nil {
			_ = rows.Close()
			return nil, leaderboard.Entry{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Close(); err != nil {
		return nil, leaderboard.Entry{}, fmt.Errorf("关闭总排行榜结果失败: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, leaderboard.Entry{}, fmt.Errorf("遍历总排行榜失败: %w", err)
	}

	row := tx.QueryRowContext(ctx, ranked+`
		SELECT rank_number, player_number, authorization_status, display_name, avatar_url, display_level, player_id
		FROM ranked WHERE player_id = ?`, playerID)
	self, _, err := scanLeaderboardEntry(row, playerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, leaderboard.Entry{}, fmt.Errorf("当前玩家未进入有效排行榜")
	}
	if err != nil {
		return nil, leaderboard.Entry{}, err
	}
	self.IsSelf = true
	if err := tx.Commit(); err != nil {
		return nil, leaderboard.Entry{}, fmt.Errorf("提交排行榜快照失败: %w", err)
	}
	return entries, self, nil
}

type leaderboardRowScanner interface{ Scan(...any) error }

func scanLeaderboardEntry(scanner leaderboardRowScanner, selfPlayerID uint64) (leaderboard.Entry, uint64, error) {
	var rankNumber uint64
	var playerNumber uint64
	var authorizationStatus string
	var displayName sql.NullString
	var avatarURL sql.NullString
	var level int
	var playerID uint64
	if err := scanner.Scan(&rankNumber, &playerNumber, &authorizationStatus, &displayName, &avatarURL, &level, &playerID); err != nil {
		return leaderboard.Entry{}, 0, err
	}
	if playerNumber == 0 || playerNumber > maxJSONSafePlayerNumber || rankNumber == 0 {
		return leaderboard.Entry{}, 0, fmt.Errorf("排行榜公开编号超出安全范围")
	}
	rank := int(rankNumber)
	name := ""
	avatar := ""
	if authorizationStatus == "authorized" {
		name = displayName.String
		avatar = avatarURL.String
	}
	if name == "" {
		name = leaderboard.DefaultDisplayName(playerNumber)
	}
	return leaderboard.Entry{
		Rank: &rank, DisplayName: name, PlayerNumber: playerNumber,
		AvatarURL: avatar, Level: level, IsSelf: playerID == selfPlayerID,
	}, playerID, nil
}

func (store *LeaderboardStore) UpdateTikTokProfile(
	ctx context.Context,
	playerID uint64,
	profile platform.AuthorizedProfile,
	now time.Time,
) (leaderboard.Profile, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return leaderboard.Profile{}, fmt.Errorf("开始资料更新事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var providerUID string
	err = tx.QueryRowContext(ctx, `SELECT provider_uid FROM player_identities WHERE player_id=? AND provider='tiktok' FOR UPDATE`, playerID).Scan(&providerUID)
	if errors.Is(err, sql.ErrNoRows) {
		return leaderboard.Profile{}, leaderboard.ErrIdentityMismatch
	}
	if err != nil {
		return leaderboard.Profile{}, fmt.Errorf("核对 TikTok 玩家身份失败: %w", err)
	}
	if providerUID != profile.ProviderUID {
		return leaderboard.Profile{}, leaderboard.ErrIdentityMismatch
	}
	if err := ensurePlayerPublicProfile(ctx, tx, playerID); err != nil {
		return leaderboard.Profile{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_public_profiles
		SET authorization_status='authorized', display_name=?, avatar_url=?, profile_updated_at=?, updated_at=?
		WHERE player_id=?`, nullableLeaderboardString(profile.DisplayName), nullableLeaderboardString(profile.AvatarURL), now, now, playerID); err != nil {
		return leaderboard.Profile{}, fmt.Errorf("保存 TikTok 排行榜资料失败: %w", err)
	}
	var playerNumber uint64
	if err := tx.QueryRowContext(ctx, `SELECT player_number FROM player_public_profiles WHERE player_id=?`, playerID).Scan(&playerNumber); err != nil {
		return leaderboard.Profile{}, fmt.Errorf("读取排行榜公开编号失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return leaderboard.Profile{}, fmt.Errorf("提交 TikTok 排行榜资料失败: %w", err)
	}
	displayName := profile.DisplayName
	if displayName == "" {
		displayName = leaderboard.DefaultDisplayName(playerNumber)
	}
	return leaderboard.Profile{Status: "authorized", DisplayName: displayName, AvatarURL: profile.AvatarURL, UpdatedAt: now}, nil
}

func (store *LeaderboardStore) InvalidateTikTokProfile(ctx context.Context, playerID uint64, now time.Time) error {
	if err := ensurePlayerPublicProfile(ctx, store.db, playerID); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE player_public_profiles
		SET authorization_status='revoked', display_name=NULL, avatar_url=NULL, profile_updated_at=?, updated_at=?
		WHERE player_id=?`, now, now, playerID); err != nil {
		return fmt.Errorf("清理失效 TikTok 排行榜资料失败: %w", err)
	}
	return nil
}

func nullableLeaderboardString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

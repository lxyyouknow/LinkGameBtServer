package mysql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/player"
)

const dailyChallengeRewardReason = "daily_challenge"

var dailyChallengeThemeIDs = [...]int{7, 13, 28, 36}

type DailyChallengeStore struct{ db *sql.DB }

func NewDailyChallengeStore(db *sql.DB) *DailyChallengeStore { return &DailyChallengeStore{db: db} }

type dailyChallengeStateRow struct {
	challengeLevel  int
	dayKey          string
	completionCount int
	replayAvailable bool
	activeAttemptID sql.NullString
	version         uint64
}

type dailyChallengeAttemptRow struct {
	attemptID         string
	dayKey            string
	challengeLevel    int
	challengeIndex    int
	mode              string
	rewardSnapshot    []byte
	status            string
	startRequestID    string
	completeRequestID sql.NullString
	completeResult    []byte
	clearSeconds      sql.NullInt64
	createdAt         sql.NullTime
	completedAt       sql.NullTime
}

func (store *DailyChallengeStore) Get(ctx context.Context, playerID uint64, dayKey string, now time.Time) (dailychallenge.State, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return dailychallenge.State{}, fmt.Errorf("开始查询每日挑战事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return dailychallenge.State{}, err
	}
	if err := ensureDailyChallengeState(ctx, tx, playerID, dayKey, now); err != nil {
		return dailychallenge.State{}, err
	}
	state, err := selectDailyChallengeState(ctx, tx, playerID, false)
	if err != nil {
		return dailychallenge.State{}, err
	}
	result, err := buildDailyChallengeState(ctx, tx, playerID, state, now)
	if err != nil {
		return dailychallenge.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return dailychallenge.State{}, fmt.Errorf("提交每日挑战查询事务失败: %w", err)
	}
	return result, nil
}

func (store *DailyChallengeStore) Start(ctx context.Context, playerID uint64, requestID, dayKey string, now time.Time) (dailychallenge.Attempt, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return dailychallenge.Attempt{}, fmt.Errorf("开始每日挑战轮次事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return dailychallenge.Attempt{}, err
	}
	if err := ensureDailyChallengeState(ctx, tx, playerID, dayKey, now); err != nil {
		return dailychallenge.Attempt{}, err
	}
	state, err := selectDailyChallengeState(ctx, tx, playerID, true)
	if err != nil {
		return dailychallenge.Attempt{}, err
	}
	level, err := selectPlayerLevel(ctx, tx, playerID, true)
	if err != nil {
		return dailychallenge.Attempt{}, err
	}
	if level < dailychallenge.UnlockLevel {
		return dailychallenge.Attempt{}, dailychallenge.ErrLocked
	}
	if reused, reusedErr := selectDailyChallengeAttemptByStartRequest(ctx, tx, playerID, requestID, false); reusedErr == nil {
		attempt, attemptErr := reused.attempt()
		if attemptErr != nil {
			return dailychallenge.Attempt{}, attemptErr
		}
		if err := tx.Commit(); err != nil {
			return dailychallenge.Attempt{}, fmt.Errorf("提交每日挑战幂等查询事务失败: %w", err)
		}
		return attempt, nil
	} else if !errors.Is(reusedErr, sql.ErrNoRows) {
		return dailychallenge.Attempt{}, reusedErr
	}
	if state.activeAttemptID.Valid {
		active, activeErr := selectDailyChallengeAttempt(ctx, tx, playerID, state.activeAttemptID.String, false)
		if activeErr == nil && active.status == "active" && active.dayKey == dayKey && active.challengeLevel == state.challengeLevel {
			attempt, attemptErr := active.attempt()
			if attemptErr != nil {
				return dailychallenge.Attempt{}, attemptErr
			}
			if err := tx.Commit(); err != nil {
				return dailychallenge.Attempt{}, fmt.Errorf("提交每日挑战续玩查询事务失败: %w", err)
			}
			return attempt, nil
		}
		if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
			return dailychallenge.Attempt{}, activeErr
		}
	}
	mode := "free"
	if state.completionCount > 0 {
		if !state.replayAvailable {
			return dailychallenge.Attempt{}, dailychallenge.ErrReplayRequired
		}
		mode = "replay"
	}
	rewards, err := createDailyChallengeRewards(ctx, tx, playerID)
	if err != nil {
		return dailychallenge.Attempt{}, err
	}
	rewardSnapshot, err := json.Marshal(rewards)
	if err != nil {
		return dailychallenge.Attempt{}, fmt.Errorf("编码每日挑战奖励快照失败: %w", err)
	}
	attemptID, err := newDailyChallengeAttemptID()
	if err != nil {
		return dailychallenge.Attempt{}, err
	}
	challengeIndex := state.challengeLevel % dailychallenge.ChallengeLevelCount
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_challenge_attempts
		(attempt_id,player_id,day_key,challenge_level,challenge_index,mode,reward_snapshot,status,start_request_id,created_at)
		VALUES (?,?,?,?,?,?,?,'active',?,?)`, attemptID, playerID, dayKey, state.challengeLevel,
		challengeIndex, mode, rewardSnapshot, requestID, now)
	if err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return dailychallenge.Attempt{}, dailychallenge.ErrStateConflict
		}
		return dailychallenge.Attempt{}, fmt.Errorf("创建每日挑战轮次失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_daily_challenge_states SET active_attempt_id=?,version=version+1,updated_at=? WHERE player_id=?`, attemptID, now, playerID); err != nil {
		return dailychallenge.Attempt{}, fmt.Errorf("保存每日挑战活动轮次失败: %w", err)
	}
	created := dailychallenge.Attempt{
		AttemptID: attemptID, DayKey: dayKey, ChallengeLevel: state.challengeLevel,
		ChallengeIndex: challengeIndex, Mode: mode, Rewards: rewards, Status: "active",
		ClearSeconds: nil, CreatedAt: now, CompletedAt: nil,
	}
	if err := tx.Commit(); err != nil {
		return dailychallenge.Attempt{}, fmt.Errorf("提交每日挑战轮次事务失败: %w", err)
	}
	return created, nil
}

func (store *DailyChallengeStore) Complete(ctx context.Context, playerID uint64, attemptID, requestID string, clearSeconds int, dayKey string, now time.Time) (dailychallenge.CompleteResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return dailychallenge.CompleteResult{}, fmt.Errorf("开始每日挑战结算事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	if err := ensureDailyChallengeState(ctx, tx, playerID, dayKey, now); err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	state, err := selectDailyChallengeState(ctx, tx, playerID, true)
	if err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	row, err := selectDailyChallengeAttempt(ctx, tx, playerID, attemptID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrAttemptNotFound
	}
	if err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	if row.status == "completed" {
		var replay dailychallenge.CompleteResult
		if len(row.completeResult) == 0 || json.Unmarshal(row.completeResult, &replay) != nil {
			return dailychallenge.CompleteResult{}, fmt.Errorf("每日挑战幂等结算结果缺失或损坏")
		}
		return replay, nil
	}
	if row.dayKey != dayKey || state.dayKey != dayKey {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrExpired
	}
	if row.challengeLevel != state.challengeLevel || !state.activeAttemptID.Valid || state.activeAttemptID.String != attemptID {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
	}
	if row.mode == "free" {
		if state.completionCount != 0 {
			return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
		}
	} else if row.mode == "replay" {
		if state.completionCount == 0 || !state.replayAvailable {
			return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
		}
	} else {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
	}
	var reusedAttemptID string
	err = tx.QueryRowContext(ctx, `SELECT attempt_id FROM daily_challenge_attempts WHERE player_id=? AND complete_request_id=? LIMIT 1`, playerID, requestID).Scan(&reusedAttemptID)
	if err == nil && reusedAttemptID != attemptID {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dailychallenge.CompleteResult{}, fmt.Errorf("检查每日挑战结算幂等键失败: %w", err)
	}
	rewards, err := row.rewards()
	if err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	if err := grantDailyChallengeRewards(ctx, tx, playerID, attemptID, rewards, now); err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	nextChallengeLevel := state.challengeLevel + 1
	nextCompletionCount := state.completionCount + 1
	if _, err := tx.ExecContext(ctx, `UPDATE player_daily_challenge_states SET challenge_level=?,completion_count=?,replay_available=FALSE,active_attempt_id=NULL,version=version+1,updated_at=? WHERE player_id=?`, nextChallengeLevel, nextCompletionCount, now, playerID); err != nil {
		return dailychallenge.CompleteResult{}, fmt.Errorf("推进每日挑战权威状态失败: %w", err)
	}
	if err := recordSeasonDailyChallengeClear(ctx, tx, playerID, attemptID, now); err != nil {
		return dailychallenge.CompleteResult{}, fmt.Errorf("推进每日挑战对应赛季任务失败: %w", err)
	}
	save, err := selectSave(ctx, tx, playerID)
	if err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	challenge := dailychallenge.State{
		DayKey: dayKey, Unlocked: true, ChallengeLevel: nextChallengeLevel,
		ChallengeIndex:  nextChallengeLevel % dailychallenge.ChallengeLevelCount,
		CompletionCount: nextCompletionCount, CompletedToday: true, ReplayAvailable: false,
		ActiveAttempt: nil, ServerTime: dailychallenge.TokyoTime(now),
	}
	result := dailychallenge.CompleteResult{AttemptID: attemptID, GrantedRewards: rewards, Challenge: challenge, Save: save}
	encoded, err := json.Marshal(result)
	if err != nil {
		return dailychallenge.CompleteResult{}, fmt.Errorf("编码每日挑战结算结果失败: %w", err)
	}
	update, err := tx.ExecContext(ctx, `UPDATE daily_challenge_attempts SET status='completed',complete_request_id=?,complete_result=?,clear_seconds=?,completed_at=? WHERE player_id=? AND attempt_id=? AND status='active'`, requestID, encoded, clearSeconds, now, playerID, attemptID)
	if err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return dailychallenge.CompleteResult{}, dailychallenge.ErrStateConflict
		}
		return dailychallenge.CompleteResult{}, fmt.Errorf("提交每日挑战轮次结算失败: %w", err)
	}
	updated, err := affected(update)
	if err != nil {
		return dailychallenge.CompleteResult{}, err
	}
	if !updated {
		return dailychallenge.CompleteResult{}, dailychallenge.ErrAttemptCompleted
	}
	if err := tx.Commit(); err != nil {
		return dailychallenge.CompleteResult{}, fmt.Errorf("提交每日挑战结算事务失败: %w", err)
	}
	return result, nil
}

func ensureDailyChallengeState(ctx context.Context, execer execContexter, playerID uint64, dayKey string, now time.Time) error {
	if _, err := execer.ExecContext(ctx, `INSERT INTO player_daily_challenge_states
		(player_id,challenge_level,day_key,completion_count,replay_available,version,created_at,updated_at)
		VALUES (?,0,?,0,FALSE,1,?,?) ON DUPLICATE KEY UPDATE player_id=player_daily_challenge_states.player_id`, playerID, dayKey, now, now); err != nil {
		return fmt.Errorf("创建每日挑战默认状态失败: %w", err)
	}
	if _, err := execer.ExecContext(ctx, `UPDATE daily_challenge_attempts AS attempt
		JOIN player_daily_challenge_states AS state ON state.player_id=attempt.player_id
		SET attempt.status='expired'
		WHERE state.player_id=? AND state.day_key<>? AND attempt.status='active'`, playerID, dayKey); err != nil {
		return fmt.Errorf("过期旧东京自然日每日挑战轮次失败: %w", err)
	}
	if _, err := execer.ExecContext(ctx, `UPDATE player_daily_challenge_states SET day_key=?,completion_count=0,replay_available=FALSE,active_attempt_id=NULL,version=version+1,updated_at=? WHERE player_id=? AND day_key<>?`, dayKey, now, playerID, dayKey); err != nil {
		return fmt.Errorf("重置东京自然日每日挑战状态失败: %w", err)
	}
	return nil
}

func selectDailyChallengeState(ctx context.Context, queryer queryContextRower, playerID uint64, forUpdate bool) (dailyChallengeStateRow, error) {
	query := `SELECT challenge_level,DATE_FORMAT(day_key,'%Y-%m-%d'),completion_count,replay_available,active_attempt_id,version FROM player_daily_challenge_states WHERE player_id=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row dailyChallengeStateRow
	err := queryer.QueryRowContext(ctx, query, playerID).Scan(&row.challengeLevel, &row.dayKey, &row.completionCount, &row.replayAvailable, &row.activeAttemptID, &row.version)
	if err != nil {
		return dailyChallengeStateRow{}, fmt.Errorf("读取每日挑战状态失败: %w", err)
	}
	return row, nil
}

func buildDailyChallengeState(ctx context.Context, queryer queryContextRower, playerID uint64, row dailyChallengeStateRow, now time.Time) (dailychallenge.State, error) {
	level, err := selectPlayerLevel(ctx, queryer, playerID, false)
	if err != nil {
		return dailychallenge.State{}, err
	}
	result := dailychallenge.State{
		DayKey: row.dayKey, Unlocked: level >= dailychallenge.UnlockLevel,
		ChallengeLevel: row.challengeLevel, ChallengeIndex: row.challengeLevel % dailychallenge.ChallengeLevelCount,
		CompletionCount: row.completionCount, CompletedToday: row.completionCount > 0,
		ReplayAvailable: row.replayAvailable, ActiveAttempt: nil, ServerTime: dailychallenge.TokyoTime(now),
	}
	if row.activeAttemptID.Valid {
		attempt, attemptErr := selectDailyChallengeAttempt(ctx, queryer, playerID, row.activeAttemptID.String, false)
		if attemptErr == nil && attempt.status == "active" && attempt.dayKey == row.dayKey {
			value, valueErr := attempt.attempt()
			if valueErr != nil {
				return dailychallenge.State{}, valueErr
			}
			result.ActiveAttempt = &value
		} else if attemptErr != nil && !errors.Is(attemptErr, sql.ErrNoRows) {
			return dailychallenge.State{}, attemptErr
		}
	}
	return result, nil
}

func selectPlayerLevel(ctx context.Context, queryer queryContextRower, playerID uint64, forUpdate bool) (int, error) {
	query := `SELECT level FROM player_saves WHERE player_id=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var level int
	if err := queryer.QueryRowContext(ctx, query, playerID).Scan(&level); err != nil {
		return 0, fmt.Errorf("读取每日挑战主线进度失败: %w", err)
	}
	return level, nil
}

func selectDailyChallengeAttempt(ctx context.Context, queryer queryContextRower, playerID uint64, attemptID string, forUpdate bool) (dailyChallengeAttemptRow, error) {
	query := dailyChallengeAttemptSelect + ` WHERE player_id=? AND attempt_id=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanDailyChallengeAttempt(queryer.QueryRowContext(ctx, query, playerID, attemptID))
}

func selectDailyChallengeAttemptByStartRequest(ctx context.Context, queryer queryContextRower, playerID uint64, requestID string, forUpdate bool) (dailyChallengeAttemptRow, error) {
	query := dailyChallengeAttemptSelect + ` WHERE player_id=? AND start_request_id=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanDailyChallengeAttempt(queryer.QueryRowContext(ctx, query, playerID, requestID))
}

const dailyChallengeAttemptSelect = `SELECT attempt_id,DATE_FORMAT(day_key,'%Y-%m-%d'),challenge_level,challenge_index,mode,reward_snapshot,status,start_request_id,complete_request_id,complete_result,clear_seconds,created_at,completed_at FROM daily_challenge_attempts`

func scanDailyChallengeAttempt(scanner rowScanner) (dailyChallengeAttemptRow, error) {
	var row dailyChallengeAttemptRow
	if err := scanner.Scan(&row.attemptID, &row.dayKey, &row.challengeLevel, &row.challengeIndex, &row.mode,
		&row.rewardSnapshot, &row.status, &row.startRequestID, &row.completeRequestID, &row.completeResult,
		&row.clearSeconds, &row.createdAt, &row.completedAt); err != nil {
		return dailyChallengeAttemptRow{}, err
	}
	return row, nil
}

func (row dailyChallengeAttemptRow) rewards() ([]dailychallenge.Reward, error) {
	var rewards []dailychallenge.Reward
	if len(row.rewardSnapshot) == 0 || json.Unmarshal(row.rewardSnapshot, &rewards) != nil || len(rewards) == 0 || len(rewards) > 3 {
		return nil, fmt.Errorf("每日挑战奖励快照缺失或损坏")
	}
	return rewards, nil
}

func (row dailyChallengeAttemptRow) attempt() (dailychallenge.Attempt, error) {
	rewards, err := row.rewards()
	if err != nil {
		return dailychallenge.Attempt{}, err
	}
	var clearSeconds *int
	if row.clearSeconds.Valid {
		value := int(row.clearSeconds.Int64)
		clearSeconds = &value
	}
	var completedAt *time.Time
	if row.completedAt.Valid {
		value := row.completedAt.Time
		completedAt = &value
	}
	return dailychallenge.Attempt{
		AttemptID: row.attemptID, DayKey: row.dayKey, ChallengeLevel: row.challengeLevel,
		ChallengeIndex: row.challengeIndex, Mode: row.mode, Rewards: rewards, Status: row.status,
		ClearSeconds: clearSeconds, CreatedAt: row.createdAt.Time, CompletedAt: completedAt,
	}, nil
}

func createDailyChallengeRewards(ctx context.Context, queryer queryContextRower, playerID uint64) ([]dailychallenge.Reward, error) {
	for _, themeID := range dailyChallengeThemeIDs {
		owned := make(map[int]struct{}, player.LimitedThemeFragmentCount)
		rows, err := queryer.QueryContext(ctx, `SELECT fragment_index FROM player_theme_fragments WHERE player_id=? AND theme_id=?`, playerID, themeID)
		if err != nil {
			return nil, fmt.Errorf("查询每日挑战主题碎片失败: %w", err)
		}
		for rows.Next() {
			var index int
			if err := rows.Scan(&index); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("读取每日挑战主题碎片失败: %w", err)
			}
			owned[index] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("关闭每日挑战主题碎片结果失败: %w", err)
		}
		if len(owned) >= player.LimitedThemeFragmentCount {
			continue
		}
		missing := make([]int, 0, player.LimitedThemeFragmentCount-len(owned))
		for index := 0; index < player.LimitedThemeFragmentCount; index++ {
			if _, exists := owned[index]; !exists {
				missing = append(missing, index)
			}
		}
		count := 3
		if len(missing) < count {
			count = len(missing)
		}
		rewards := make([]dailychallenge.Reward, 0, count)
		for len(rewards) < count {
			selectedIndex, err := secureRandomIndex(len(missing))
			if err != nil {
				return nil, err
			}
			fragmentIndex := missing[selectedIndex]
			missing[selectedIndex] = missing[len(missing)-1]
			missing = missing[:len(missing)-1]
			theme, fragment := themeID, fragmentIndex
			rewards = append(rewards, dailychallenge.Reward{Type: "theme_fragment", ThemeID: &theme, FragmentIndex: &fragment, Quantity: 1})
		}
		return rewards, nil
	}
	return []dailychallenge.Reward{{Type: "coins", Quantity: 120}}, nil
}

func grantDailyChallengeRewards(ctx context.Context, tx *sql.Tx, playerID uint64, attemptID string, rewards []dailychallenge.Reward, now time.Time) error {
	var coinTotal int64
	for index, reward := range rewards {
		switch reward.Type {
		case "coins":
			if reward.Quantity != 120 || reward.ThemeID != nil || reward.FragmentIndex != nil {
				return dailychallenge.ErrStateConflict
			}
			coinTotal += reward.Quantity
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_coin_mutations (player_id,mutation_id,delta,reason,client_version,created_at) VALUES (?,?,?,?,?,?)`, playerID, fmt.Sprintf("dc:%s:coin", attemptID), reward.Quantity, dailyChallengeRewardReason, "", now); err != nil {
				return fmt.Errorf("记录每日挑战金币流水失败: %w", err)
			}
		case "theme_fragment":
			if reward.Quantity != 1 || reward.ThemeID == nil || reward.FragmentIndex == nil ||
				!containsDailyChallengeTheme(*reward.ThemeID) || *reward.FragmentIndex < 0 || *reward.FragmentIndex >= player.LimitedThemeFragmentCount {
				return dailychallenge.ErrStateConflict
			}
			mutationID := fmt.Sprintf("dc:%s:theme:%d", attemptID, index)
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_theme_mutations (player_id,mutation_id,theme_id,fragment_index,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`, playerID, mutationID, *reward.ThemeID, *reward.FragmentIndex, dailyChallengeRewardReason, now); err != nil {
				return fmt.Errorf("记录每日挑战主题流水失败: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_fragments (player_id,theme_id,fragment_index,acquired_at) VALUES (?,?,?,?)`, playerID, *reward.ThemeID, *reward.FragmentIndex, now); err != nil {
				return fmt.Errorf("写入每日挑战主题碎片失败: %w", err)
			}
		default:
			return dailychallenge.ErrStateConflict
		}
	}
	var coins int64
	if err := tx.QueryRowContext(ctx, `SELECT coins FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&coins); err != nil {
		return fmt.Errorf("锁定每日挑战金币存档失败: %w", err)
	}
	if coinTotal > 0 && coins > player.MaxCoins-coinTotal {
		return player.ErrCoinLimitExceeded
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_saves SET coins=coins+?,revision=revision+1,updated_at=? WHERE player_id=?`, coinTotal, now, playerID); err != nil {
		return fmt.Errorf("更新每日挑战权威存档失败: %w", err)
	}
	return nil
}

func containsDailyChallengeTheme(themeID int) bool {
	for _, candidate := range dailyChallengeThemeIDs {
		if candidate == themeID {
			return true
		}
	}
	return false
}

func newDailyChallengeAttemptID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成每日挑战轮次 ID 失败: %w", err)
	}
	return "dc_" + hex.EncodeToString(value), nil
}

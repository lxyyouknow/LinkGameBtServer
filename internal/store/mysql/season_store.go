package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

const seasonRewardReason = "season_reward"

type SeasonStore struct{ db *sql.DB }

func NewSeasonStore(db *sql.DB) *SeasonStore { return &SeasonStore{db: db} }

type seasonDayRow struct {
	day             int
	clearedLevels   int
	loginClaimedAt  sql.NullTime
	clearClaimedAt  sql.NullTime
	rewardClaimedAt sql.NullTime
	rewardSnapshot  []byte
}

func (store *SeasonStore) Get(
	ctx context.Context,
	playerID uint64,
	window season.Window,
	now time.Time,
) (season.State, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return season.State{}, fmt.Errorf("开始查询赛季事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return season.State{}, err
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return season.State{}, err
	}
	if window.RewardPeriodOpen {
		if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
			return season.State{}, err
		}
	}
	result, err := buildSeasonState(ctx, tx, playerID, window, now)
	if err != nil {
		return season.State{}, err
	}
	if err := tx.Commit(); err != nil {
		return season.State{}, fmt.Errorf("提交查询赛季事务失败: %w", err)
	}
	return result, nil
}

func (store *SeasonStore) RecordClear(
	ctx context.Context,
	playerID uint64,
	requestID string,
	completionID string,
	level int,
	window season.Window,
	now time.Time,
) (season.ProgressResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return season.ProgressResult{}, fmt.Errorf("开始记录赛季通关事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return season.ProgressResult{}, err
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return season.ProgressResult{}, err
	}
	if !window.RewardPeriodOpen {
		result, resultErr := selectSeasonProgress(ctx, tx, playerID, window, 0)
		if resultErr != nil {
			return season.ProgressResult{}, resultErr
		}
		if err := tx.Commit(); err != nil {
			return season.ProgressResult{}, fmt.Errorf("提交赛季关闭期通关查询失败: %w", err)
		}
		return result, nil
	}
	if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
		return season.ProgressResult{}, err
	}
	levelCopy := level
	result, err := recordSeasonClearTx(ctx, tx, playerID, requestID, completionID, "main", &levelCopy, window, now)
	if err != nil {
		return season.ProgressResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return season.ProgressResult{}, fmt.Errorf("提交赛季通关事务失败: %w", err)
	}
	return result, nil
}

func (store *SeasonStore) Claim(
	ctx context.Context,
	playerID uint64,
	task season.Task,
	requestID string,
	expectedSeasonKey string,
	expectedDayKey string,
	window season.Window,
	now time.Time,
) (season.ClaimResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return season.ClaimResult{}, fmt.Errorf("开始领取赛季任务事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return season.ClaimResult{}, err
	}
	// 统一采用“玩家存档 → 赛季日状态”的加锁顺序。每日挑战结算也会在同一事务内
	// 更新这两类数据，顺序一致可避免它与赛季领奖并发时形成循环等待。
	var saveRevision uint64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&saveRevision); err != nil {
		return season.ClaimResult{}, fmt.Errorf("锁定赛季领奖权威存档失败: %w", err)
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return season.ClaimResult{}, err
	}
	if window.RewardPeriodOpen {
		if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
			return season.ClaimResult{}, err
		}
		if _, err := selectSeasonDay(ctx, tx, playerID, window.DayKey, true); err != nil {
			return season.ClaimResult{}, err
		}
	}
	if replay, found, replayErr := selectSeasonClaimRequest(ctx, tx, playerID, requestID, task, expectedSeasonKey, expectedDayKey); replayErr != nil {
		return season.ClaimResult{}, replayErr
	} else if found {
		if err := tx.Commit(); err != nil {
			return season.ClaimResult{}, fmt.Errorf("提交赛季领取幂等查询失败: %w", err)
		}
		return replay, nil
	}
	if expectedSeasonKey != window.SeasonKey || expectedDayKey != window.DayKey {
		return season.ClaimResult{}, season.ErrDayChanged
	}
	if !window.RewardPeriodOpen {
		return season.ClaimResult{}, season.ErrRewardPeriodClosed
	}
	dayRow, err := selectSeasonDay(ctx, tx, playerID, window.DayKey, true)
	if err != nil {
		return season.ClaimResult{}, err
	}
	if dayRow.rewardClaimedAt.Valid {
		return season.ClaimResult{}, season.ErrDayAlreadyRewarded
	}
	switch task {
	case season.TaskLogin:
		if dayRow.loginClaimedAt.Valid {
			return season.ClaimResult{}, season.ErrTaskAlreadyClaimed
		}
		dayRow.loginClaimedAt = sql.NullTime{Time: now, Valid: true}
	case season.TaskClearLevels:
		if dayRow.clearClaimedAt.Valid {
			return season.ClaimResult{}, season.ErrTaskAlreadyClaimed
		}
		if dayRow.clearedLevels < season.ClearLevelTarget {
			return season.ClaimResult{}, season.ErrTaskNotReady
		}
		dayRow.clearClaimedAt = sql.NullTime{Time: now, Valid: true}
	default:
		return season.ClaimResult{}, season.ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_season_days SET
		login_claimed_at=?,clear_claimed_at=?,updated_at=? WHERE player_id=? AND day_key=?`,
		nullableTime(dayRow.loginClaimedAt), nullableTime(dayRow.clearClaimedAt), now, playerID, window.DayKey); err != nil {
		return season.ClaimResult{}, fmt.Errorf("更新赛季任务领取状态失败: %w", err)
	}
	rewards := make([]season.Reward, 0)
	if dayRow.loginClaimedAt.Valid && dayRow.clearClaimedAt.Valid {
		rewards, err = decodeSeasonRewards(dayRow.rewardSnapshot, window.Config, window.Day)
		if err != nil {
			return season.ClaimResult{}, err
		}
		if err := grantSeasonRewards(ctx, tx, playerID, window, rewards, now); err != nil {
			return season.ClaimResult{}, err
		}
		dayRow.rewardClaimedAt = sql.NullTime{Time: now, Valid: true}
		if _, err := tx.ExecContext(ctx, `UPDATE player_season_days SET reward_claimed_at=?,updated_at=? WHERE player_id=? AND day_key=?`, now, now, playerID, window.DayKey); err != nil {
			return season.ClaimResult{}, fmt.Errorf("标记赛季完整奖励失败: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_season_months SET state_version=state_version+1,updated_at=? WHERE player_id=? AND season_key=?`, now, playerID, window.SeasonKey); err != nil {
		return season.ClaimResult{}, fmt.Errorf("推进赛季状态版本失败: %w", err)
	}
	claimState, err := buildSeasonClaimState(ctx, tx, playerID, window, dayRow)
	if err != nil {
		return season.ClaimResult{}, err
	}
	save, err := selectSave(ctx, tx, playerID)
	if err != nil {
		return season.ClaimResult{}, err
	}
	result := season.ClaimResult{AcceptedTask: task, GrantedRewards: rewards, Season: claimState, Save: save}
	encoded, err := json.Marshal(result)
	if err != nil {
		return season.ClaimResult{}, fmt.Errorf("编码赛季领取幂等结果失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_season_task_claims
		(player_id,request_id,task,season_key,day_key,result,created_at) VALUES (?,?,?,?,?,?,?)`,
		playerID, requestID, task, expectedSeasonKey, expectedDayKey, encoded, now); err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return season.ClaimResult{}, season.ErrRequestIDConflict
		}
		return season.ClaimResult{}, fmt.Errorf("保存赛季领取幂等结果失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return season.ClaimResult{}, fmt.Errorf("提交赛季任务领取事务失败: %w", err)
	}
	return result, nil
}

func (store *SeasonStore) InspectPlayer(
	ctx context.Context,
	publicPlayerID string,
	window season.Window,
	now time.Time,
) (season.AdminAudit, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: false})
	if err != nil {
		return season.AdminAudit{}, fmt.Errorf("开始查询赛季审计事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var playerID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM players WHERE public_id=?`, publicPlayerID).Scan(&playerID); errors.Is(err, sql.ErrNoRows) {
		return season.AdminAudit{}, season.ErrPlayerNotFound
	} else if err != nil {
		return season.AdminAudit{}, fmt.Errorf("查询赛季审计玩家失败: %w", err)
	}
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return season.AdminAudit{}, err
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return season.AdminAudit{}, err
	}
	if window.RewardPeriodOpen {
		if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
			return season.AdminAudit{}, err
		}
	}
	state, err := buildSeasonState(ctx, tx, playerID, window, now)
	if err != nil {
		return season.AdminAudit{}, err
	}
	audit := season.AdminAudit{
		PlayerID: publicPlayerID, State: state,
		ClearEvents: make([]season.AdminClearEvent, 0), Claims: make([]season.AdminClaim, 0),
		MakeupClaims: make([]season.AdminMakeupClaim, 0), RewardMutations: make([]string, 0),
	}
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(makeup_recovery_day_key,'%Y-%m-%d')
		FROM player_season_months WHERE player_id=? AND season_key=?`, playerID, window.SeasonKey).
		Scan(&audit.MakeupRecoveryDayKey); err != nil {
		return season.AdminAudit{}, fmt.Errorf("读取赛季补签恢复日失败: %w", err)
	}
	eventRows, err := tx.QueryContext(ctx, `SELECT request_id,completion_id,source,level,DATE_FORMAT(day_key,'%Y-%m-%d'),created_at
		FROM player_season_clear_events WHERE player_id=? AND DATE_FORMAT(day_key,'%Y-%m')=? ORDER BY created_at`,
		playerID, window.SeasonKey)
	if err != nil {
		return season.AdminAudit{}, fmt.Errorf("查询赛季审计通关事件失败: %w", err)
	}
	for eventRows.Next() {
		var item season.AdminClearEvent
		var level sql.NullInt64
		if err := eventRows.Scan(&item.RequestID, &item.CompletionID, &item.Source, &level, &item.DayKey, &item.CreatedAt); err != nil {
			_ = eventRows.Close()
			return season.AdminAudit{}, fmt.Errorf("读取赛季审计通关事件失败: %w", err)
		}
		if level.Valid {
			value := int(level.Int64)
			item.Level = &value
		}
		audit.ClearEvents = append(audit.ClearEvents, item)
	}
	if err := eventRows.Err(); err != nil {
		_ = eventRows.Close()
		return season.AdminAudit{}, fmt.Errorf("遍历赛季审计通关事件失败: %w", err)
	}
	if err := eventRows.Close(); err != nil {
		return season.AdminAudit{}, fmt.Errorf("关闭赛季审计通关事件失败: %w", err)
	}
	claimRows, err := tx.QueryContext(ctx, `SELECT request_id,task,DATE_FORMAT(day_key,'%Y-%m-%d'),result,created_at
		FROM player_season_task_claims WHERE player_id=? AND season_key=? ORDER BY created_at`, playerID, window.SeasonKey)
	if err != nil {
		return season.AdminAudit{}, fmt.Errorf("查询赛季审计领取请求失败: %w", err)
	}
	for claimRows.Next() {
		var item season.AdminClaim
		var encoded []byte
		if err := claimRows.Scan(&item.RequestID, &item.Task, &item.DayKey, &encoded, &item.CreatedAt); err != nil {
			_ = claimRows.Close()
			return season.AdminAudit{}, fmt.Errorf("读取赛季审计领取请求失败: %w", err)
		}
		if len(encoded) == 0 || json.Unmarshal(encoded, &item.Result) != nil {
			_ = claimRows.Close()
			return season.AdminAudit{}, fmt.Errorf("赛季审计领取结果缺失或损坏")
		}
		audit.Claims = append(audit.Claims, item)
	}
	if err := claimRows.Err(); err != nil {
		_ = claimRows.Close()
		return season.AdminAudit{}, fmt.Errorf("遍历赛季审计领取请求失败: %w", err)
	}
	if err := claimRows.Close(); err != nil {
		return season.AdminAudit{}, fmt.Errorf("关闭赛季审计领取请求失败: %w", err)
	}
	makeupRows, err := tx.QueryContext(ctx, `SELECT target_day,DATE_FORMAT(target_day_key,'%Y-%m-%d'),session_id,
		ad_attempt_id,business_key,reward_snapshot,save_revision,created_at FROM player_season_makeup_claims
		WHERE player_id=? AND season_key=? ORDER BY created_at`, playerID, window.SeasonKey)
	if err != nil {
		return season.AdminAudit{}, fmt.Errorf("查询赛季补签审计失败: %w", err)
	}
	for makeupRows.Next() {
		var item season.AdminMakeupClaim
		var snapshot []byte
		if err := makeupRows.Scan(&item.TargetDay, &item.TargetDayKey, &item.SessionID, &item.AdAttemptID,
			&item.BusinessKey, &snapshot, &item.SaveRevision, &item.CreatedAt); err != nil {
			_ = makeupRows.Close()
			return season.AdminAudit{}, fmt.Errorf("读取赛季补签审计失败: %w", err)
		}
		if len(snapshot) == 0 || json.Unmarshal(snapshot, &item.RewardSnapshot) != nil {
			_ = makeupRows.Close()
			return season.AdminAudit{}, fmt.Errorf("赛季补签奖励快照缺失或损坏")
		}
		audit.MakeupClaims = append(audit.MakeupClaims, item)
	}
	if err := makeupRows.Err(); err != nil {
		_ = makeupRows.Close()
		return season.AdminAudit{}, fmt.Errorf("遍历赛季补签审计失败: %w", err)
	}
	if err := makeupRows.Close(); err != nil {
		return season.AdminAudit{}, fmt.Errorf("关闭赛季补签审计失败: %w", err)
	}
	mutationPattern := "season:" + window.SeasonKey + "-%"
	for _, query := range []string{
		`SELECT mutation_id FROM player_prop_mutations WHERE player_id=? AND mutation_id LIKE ? ORDER BY created_at`,
		`SELECT mutation_id FROM player_theme_mutations WHERE player_id=? AND mutation_id LIKE ? ORDER BY created_at`,
	} {
		rows, queryErr := tx.QueryContext(ctx, query, playerID, mutationPattern)
		if queryErr != nil {
			return season.AdminAudit{}, fmt.Errorf("查询赛季审计奖励流水失败: %w", queryErr)
		}
		for rows.Next() {
			var mutationID string
			if scanErr := rows.Scan(&mutationID); scanErr != nil {
				_ = rows.Close()
				return season.AdminAudit{}, fmt.Errorf("读取赛季审计奖励流水失败: %w", scanErr)
			}
			audit.RewardMutations = append(audit.RewardMutations, mutationID)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return season.AdminAudit{}, fmt.Errorf("遍历赛季审计奖励流水失败: %w", rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return season.AdminAudit{}, fmt.Errorf("关闭赛季审计奖励流水失败: %w", closeErr)
		}
	}
	if err := tx.Commit(); err != nil {
		return season.AdminAudit{}, fmt.Errorf("提交赛季审计查询失败: %w", err)
	}
	return audit, nil
}

// recordSeasonDailyChallengeClear 与每日挑战结算复用同一事务，避免奖励已到账但赛季计数丢失。
func recordSeasonDailyChallengeClear(
	ctx context.Context,
	tx *sql.Tx,
	playerID uint64,
	attemptID string,
	now time.Time,
) error {
	window, err := season.WindowFor(now)
	if err != nil {
		return err
	}
	if !window.RewardPeriodOpen {
		return nil
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return err
	}
	if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
		return err
	}
	identifier := "daily:" + attemptID
	_, err = recordSeasonClearTx(ctx, tx, playerID, identifier, identifier, "daily", nil, window, now)
	return err
}

func recordSeasonClearTx(
	ctx context.Context,
	tx *sql.Tx,
	playerID uint64,
	requestID string,
	completionID string,
	source string,
	level *int,
	window season.Window,
	now time.Time,
) (season.ProgressResult, error) {
	dayRow, err := selectSeasonDay(ctx, tx, playerID, window.DayKey, true)
	if err != nil {
		return season.ProgressResult{}, err
	}
	var existingRequestID, existingCompletionID, existingSource string
	var existingLevel sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT request_id,completion_id,source,level FROM player_season_clear_events
		WHERE player_id=? AND (request_id=? OR completion_id=?) LIMIT 1`, playerID, requestID, completionID).
		Scan(&existingRequestID, &existingCompletionID, &existingSource, &existingLevel)
	if err == nil {
		if existingRequestID != requestID || existingCompletionID != completionID || existingSource != source || !sameNullableLevel(existingLevel, level) {
			return season.ProgressResult{}, season.ErrRequestIDConflict
		}
		return selectSeasonProgress(ctx, tx, playerID, window, dayRow.clearedLevels)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return season.ProgressResult{}, fmt.Errorf("检查赛季通关幂等事件失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_season_clear_events
		(player_id,request_id,completion_id,source,level,day_key,created_at) VALUES (?,?,?,?,?,?,?)`,
		playerID, requestID, completionID, source, level, window.DayKey, now); err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return season.ProgressResult{}, season.ErrRequestIDConflict
		}
		return season.ProgressResult{}, fmt.Errorf("记录赛季通关幂等事件失败: %w", err)
	}
	if dayRow.clearedLevels < season.ClearLevelTarget {
		dayRow.clearedLevels++
		if _, err := tx.ExecContext(ctx, `UPDATE player_season_days SET cleared_levels=?,updated_at=? WHERE player_id=? AND day_key=?`, dayRow.clearedLevels, now, playerID, window.DayKey); err != nil {
			return season.ProgressResult{}, fmt.Errorf("推进赛季通关次数失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE player_season_months SET state_version=state_version+1,updated_at=? WHERE player_id=? AND season_key=?`, now, playerID, window.SeasonKey); err != nil {
			return season.ProgressResult{}, fmt.Errorf("推进赛季状态版本失败: %w", err)
		}
	}
	return selectSeasonProgress(ctx, tx, playerID, window, dayRow.clearedLevels)
}

type seasonExecer interface {
	execContexter
	queryContextRower
}

func ensureSeasonMonth(ctx context.Context, execer seasonExecer, playerID uint64, window season.Window, now time.Time) error {
	if _, err := execer.ExecContext(ctx, `INSERT INTO player_season_months
		(player_id,season_key,config_version,config_id,state_version,makeup_remaining,makeup_recovery_day_key,created_at,updated_at)
		VALUES (?,?,?,?,1,?,?,?,?) ON DUPLICATE KEY UPDATE player_id=player_season_months.player_id`,
		playerID, window.SeasonKey, season.ConfigVersion, window.Config.ID, season.MaxMakeupCount, window.DayKey, now, now); err != nil {
		return fmt.Errorf("创建玩家赛季月状态失败: %w", err)
	}
	var configVersion string
	var configID int
	if err := execer.QueryRowContext(ctx, `SELECT config_version,config_id FROM player_season_months WHERE player_id=? AND season_key=?`, playerID, window.SeasonKey).Scan(&configVersion, &configID); err != nil {
		return fmt.Errorf("读取玩家赛季月配置失败: %w", err)
	}
	if configVersion != season.ConfigVersion || configID != window.Config.ID {
		return season.ErrConfigUnavailable
	}
	return recoverSeasonMakeup(ctx, execer, playerID, window, now)
}

func recoverSeasonMakeup(ctx context.Context, execer seasonExecer, playerID uint64, window season.Window, now time.Time) error {
	remaining, recoveryDayKey, err := selectSeasonMakeupBalance(ctx, execer, playerID, window.SeasonKey, true)
	if err != nil {
		return err
	}
	if recoveryDayKey == window.DayKey {
		return nil
	}
	if recoveryDayKey != "" && recoveryDayKey > window.DayKey {
		return season.ErrStateConflict
	}
	nextRemaining := remaining
	if recoveryDayKey != "" && remaining < season.MaxMakeupCount {
		nextRemaining++
	}
	if _, err := execer.ExecContext(ctx, `UPDATE player_season_months SET makeup_remaining=?,makeup_recovery_day_key=?,
		state_version=state_version+1,updated_at=? WHERE player_id=? AND season_key=?`,
		nextRemaining, window.DayKey, now, playerID, window.SeasonKey); err != nil {
		return fmt.Errorf("恢复赛季补签次数失败: %w", err)
	}
	return nil
}

func selectSeasonMakeupBalance(
	ctx context.Context,
	queryer queryContextRower,
	playerID uint64,
	seasonKey string,
	forUpdate bool,
) (int, string, error) {
	query := `SELECT makeup_remaining,DATE_FORMAT(makeup_recovery_day_key,'%Y-%m-%d')
		FROM player_season_months WHERE player_id=? AND season_key=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var remaining int
	var recoveryDay sql.NullString
	if err := queryer.QueryRowContext(ctx, query, playerID, seasonKey).Scan(&remaining, &recoveryDay); err != nil {
		return 0, "", fmt.Errorf("读取赛季补签次数失败: %w", err)
	}
	if remaining < 0 || remaining > season.MaxMakeupCount {
		return 0, "", season.ErrStateConflict
	}
	return remaining, recoveryDay.String, nil
}

func ensureSeasonDay(ctx context.Context, execer seasonExecer, playerID uint64, window season.Window, now time.Time) error {
	rewardDays, err := season.RewardDays(window.Config)
	if err != nil || window.Day < 1 || window.Day > len(rewardDays) {
		return season.ErrConfigUnavailable
	}
	snapshot, err := json.Marshal(rewardDays[window.Day-1].Rewards)
	if err != nil {
		return fmt.Errorf("编码赛季奖励快照失败: %w", err)
	}
	if _, err := execer.ExecContext(ctx, `INSERT INTO player_season_days
		(player_id,season_key,day_key,day,cleared_levels,reward_snapshot,created_at,updated_at)
		VALUES (?,?,?,?,0,?,?,?) ON DUPLICATE KEY UPDATE player_id=player_season_days.player_id`,
		playerID, window.SeasonKey, window.DayKey, window.Day, snapshot, now, now); err != nil {
		return fmt.Errorf("创建玩家赛季日状态失败: %w", err)
	}
	return nil
}

func seasonWindowForRewardDay(window season.Window, day int) season.Window {
	target := window
	target.Day = day
	target.DayKey = fmt.Sprintf("%s-%02d", window.SeasonKey, day)
	target.RewardPeriodOpen = true
	return target
}

func buildSeasonState(
	ctx context.Context,
	queryer queryContextRower,
	playerID uint64,
	window season.Window,
	now time.Time,
) (season.State, error) {
	var stateVersion uint64
	var makeupRemaining int
	if err := queryer.QueryRowContext(ctx, `SELECT state_version,makeup_remaining FROM player_season_months WHERE player_id=? AND season_key=?`, playerID, window.SeasonKey).Scan(&stateVersion, &makeupRemaining); err != nil {
		return season.State{}, fmt.Errorf("读取赛季状态版本失败: %w", err)
	}
	claimedDays, err := selectSeasonClaimedDays(ctx, queryer, playerID, window.SeasonKey)
	if err != nil {
		return season.State{}, err
	}
	rewardDays, err := season.RewardDays(window.Config)
	if err != nil {
		return season.State{}, err
	}
	result := season.State{
		ServerTime: now, Timezone: "Asia/Tokyo", SeasonKey: window.SeasonKey,
		ConfigVersion: season.ConfigVersion, ConfigID: window.Config.ID, ThemeID: window.Config.ThemeID,
		StartsAt: window.StartsAt, EndsAt: window.EndsAt, NextResetAt: window.NextResetAt,
		StateVersion: stateVersion, ClaimedDays: claimedDays, MakeupRemaining: makeupRemaining, RewardDays: rewardDays,
	}
	if window.RewardPeriodOpen {
		dayRow, dayErr := selectSeasonDay(ctx, queryer, playerID, window.DayKey, false)
		if dayErr != nil {
			return season.State{}, dayErr
		}
		day := window.Day
		result.CurrentRewardDay = &day
		result.Today = &season.TodayState{
			DayKey: window.DayKey, Day: window.Day, ClearedLevels: dayRow.clearedLevels,
			Login: season.TaskProgress{
				Target: 1, Progress: 1, Claimable: !dayRow.loginClaimedAt.Valid,
				ClaimedAt: nullableTime(dayRow.loginClaimedAt),
			},
			ClearLevels: season.TaskProgress{
				Target: season.ClearLevelTarget, Progress: dayRow.clearedLevels,
				Claimable: dayRow.clearedLevels >= season.ClearLevelTarget && !dayRow.clearClaimedAt.Valid,
				ClaimedAt: nullableTime(dayRow.clearClaimedAt),
			},
			RewardClaimedAt: nullableTime(dayRow.rewardClaimedAt),
		}
	}
	return result, nil
}

func selectSeasonDay(ctx context.Context, queryer queryContextRower, playerID uint64, dayKey string, forUpdate bool) (seasonDayRow, error) {
	query := `SELECT day,cleared_levels,login_claimed_at,clear_claimed_at,reward_claimed_at,reward_snapshot
		FROM player_season_days WHERE player_id=? AND day_key=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var row seasonDayRow
	if err := queryer.QueryRowContext(ctx, query, playerID, dayKey).Scan(
		&row.day, &row.clearedLevels, &row.loginClaimedAt, &row.clearClaimedAt,
		&row.rewardClaimedAt, &row.rewardSnapshot,
	); err != nil {
		return seasonDayRow{}, fmt.Errorf("读取玩家赛季日状态失败: %w", err)
	}
	return row, nil
}

func selectSeasonClaimedDays(ctx context.Context, queryer queryContextRower, playerID uint64, seasonKey string) ([]int, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT day FROM player_season_days
		WHERE player_id=? AND season_key=? AND reward_claimed_at IS NOT NULL ORDER BY day`, playerID, seasonKey)
	if err != nil {
		return nil, fmt.Errorf("查询赛季已领取日期失败: %w", err)
	}
	defer rows.Close()
	claimed := make([]int, 0, season.RewardDayCount)
	for rows.Next() {
		var day int
		if err := rows.Scan(&day); err != nil {
			return nil, fmt.Errorf("读取赛季已领取日期失败: %w", err)
		}
		claimed = append(claimed, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历赛季已领取日期失败: %w", err)
	}
	return claimed, nil
}

func selectSeasonProgress(ctx context.Context, queryer queryContextRower, playerID uint64, window season.Window, clearedLevels int) (season.ProgressResult, error) {
	var stateVersion uint64
	if err := queryer.QueryRowContext(ctx, `SELECT state_version FROM player_season_months WHERE player_id=? AND season_key=?`, playerID, window.SeasonKey).Scan(&stateVersion); err != nil {
		return season.ProgressResult{}, fmt.Errorf("读取赛季通关状态版本失败: %w", err)
	}
	return season.ProgressResult{
		SeasonKey: window.SeasonKey, DayKey: window.DayKey, StateVersion: stateVersion,
		ClearedLevels: clearedLevels, Claimable: window.RewardPeriodOpen && clearedLevels >= season.ClearLevelTarget,
	}, nil
}

func buildSeasonClaimState(ctx context.Context, queryer queryContextRower, playerID uint64, window season.Window, dayRow seasonDayRow) (season.ClaimState, error) {
	var stateVersion uint64
	if err := queryer.QueryRowContext(ctx, `SELECT state_version FROM player_season_months WHERE player_id=? AND season_key=?`, playerID, window.SeasonKey).Scan(&stateVersion); err != nil {
		return season.ClaimState{}, fmt.Errorf("读取赛季领取状态版本失败: %w", err)
	}
	claimedDays, err := selectSeasonClaimedDays(ctx, queryer, playerID, window.SeasonKey)
	if err != nil {
		return season.ClaimState{}, err
	}
	return season.ClaimState{
		SeasonKey: window.SeasonKey, DayKey: window.DayKey, StateVersion: stateVersion,
		ClearedLevels: dayRow.clearedLevels, LoginClaimedAt: nullableTime(dayRow.loginClaimedAt),
		ClearClaimedAt: nullableTime(dayRow.clearClaimedAt), RewardClaimedAt: nullableTime(dayRow.rewardClaimedAt),
		ClaimedDays: claimedDays,
	}, nil
}

func selectSeasonClaimRequest(
	ctx context.Context,
	queryer queryContextRower,
	playerID uint64,
	requestID string,
	task season.Task,
	seasonKey string,
	dayKey string,
) (season.ClaimResult, bool, error) {
	var storedTask season.Task
	var storedSeasonKey, storedDayKey string
	var encoded []byte
	err := queryer.QueryRowContext(ctx, `SELECT task,season_key,DATE_FORMAT(day_key,'%Y-%m-%d'),result FROM player_season_task_claims
		WHERE player_id=? AND request_id=?`, playerID, requestID).
		Scan(&storedTask, &storedSeasonKey, &storedDayKey, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return season.ClaimResult{}, false, nil
	}
	if err != nil {
		return season.ClaimResult{}, false, fmt.Errorf("读取赛季领取幂等结果失败: %w", err)
	}
	if storedTask != task || storedSeasonKey != seasonKey || storedDayKey != dayKey {
		return season.ClaimResult{}, false, season.ErrRequestIDConflict
	}
	var result season.ClaimResult
	if len(encoded) == 0 || json.Unmarshal(encoded, &result) != nil {
		return season.ClaimResult{}, false, fmt.Errorf("赛季领取幂等结果缺失或损坏")
	}
	return result, true, nil
}

func decodeSeasonRewards(snapshot []byte, config season.Config, day int) ([]season.Reward, error) {
	var rewards []season.Reward
	if len(snapshot) == 0 || json.Unmarshal(snapshot, &rewards) != nil {
		return nil, season.ErrConfigUnavailable
	}
	rewardDays, err := season.RewardDays(config)
	if err != nil || day < 1 || day > len(rewardDays) || !reflect.DeepEqual(rewards, rewardDays[day-1].Rewards) {
		return nil, season.ErrConfigUnavailable
	}
	return rewards, nil
}

func grantSeasonRewards(ctx context.Context, tx *sql.Tx, playerID uint64, window season.Window, rewards []season.Reward, now time.Time) error {
	var hints, shuffles, removes int64
	for _, reward := range rewards {
		switch reward.Type {
		case "prop":
			if reward.Quantity <= 0 || reward.ThemeID != nil || reward.FragmentIndex != nil {
				return season.ErrConfigUnavailable
			}
			var target *int64
			switch reward.PropType {
			case "hint":
				target = &hints
			case "shuffle":
				target = &shuffles
			case "remove":
				target = &removes
			default:
				return season.ErrConfigUnavailable
			}
			*target += reward.Quantity
			mutationID := fmt.Sprintf("season:%s:prop:%s", window.DayKey, reward.PropType)
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_prop_mutations
				(player_id,mutation_id,prop_type,delta,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
				playerID, mutationID, reward.PropType, reward.Quantity, seasonRewardReason, now); err != nil {
				return fmt.Errorf("记录赛季道具流水失败: %w", err)
			}
		case "theme_fragment":
			if reward.Quantity != 1 || reward.PropType != "" || reward.ThemeID == nil || reward.FragmentIndex == nil ||
				*reward.ThemeID != window.Config.ThemeID || !player.IsLimitedTheme(*reward.ThemeID) ||
				*reward.FragmentIndex < 0 || *reward.FragmentIndex >= player.LimitedThemeFragmentCount {
				return season.ErrConfigUnavailable
			}
			mutationID := fmt.Sprintf("season:%s:theme:%d", window.DayKey, *reward.FragmentIndex)
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_theme_mutations
				(player_id,mutation_id,theme_id,fragment_index,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
				playerID, mutationID, *reward.ThemeID, *reward.FragmentIndex, seasonRewardReason, now); err != nil {
				return fmt.Errorf("记录赛季主题流水失败: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_fragments
				(player_id,theme_id,fragment_index,acquired_at) VALUES (?,?,?,?)`,
				playerID, *reward.ThemeID, *reward.FragmentIndex, now); err != nil {
				return fmt.Errorf("写入赛季主题碎片失败: %w", err)
			}
		default:
			return season.ErrConfigUnavailable
		}
	}
	var currentHints, currentShuffles, currentRemoves int64
	if err := tx.QueryRowContext(ctx, `SELECT hint_count,shuffle_count,remove_count FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).
		Scan(&currentHints, &currentShuffles, &currentRemoves); err != nil {
		return fmt.Errorf("锁定赛季权威存档失败: %w", err)
	}
	if currentHints > player.MaxPropCount-hints || currentShuffles > player.MaxPropCount-shuffles || currentRemoves > player.MaxPropCount-removes {
		return player.ErrPropLimitExceeded
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_saves SET hint_count=hint_count+?,shuffle_count=shuffle_count+?,
		remove_count=remove_count+?,revision=revision+1,updated_at=? WHERE player_id=?`,
		hints, shuffles, removes, now, playerID); err != nil {
		return fmt.Errorf("更新赛季权威存档失败: %w", err)
	}
	return nil
}

func sameNullableLevel(existing sql.NullInt64, level *int) bool {
	if level == nil {
		return !existing.Valid
	}
	return existing.Valid && existing.Int64 == int64(*level)
}

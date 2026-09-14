package mysql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

const adRewardReason = "ad_reward"

type AdRewardStore struct{ db *sql.DB }

func NewAdRewardStore(db *sql.DB) *AdRewardStore { return &AdRewardStore{db: db} }

func (store *AdRewardStore) Create(ctx context.Context, playerID uint64, placement adreward.Placement, businessKey string, now, expiresAt time.Time) (adreward.Session, error) {
	if placement == adreward.PlacementDailyGift {
		return store.createDailyGiftSession(ctx, playerID, businessKey, now, expiresAt)
	}
	if placement == adreward.PlacementDailyChallengeReplay {
		return store.createDailyChallengeReplaySession(ctx, playerID, businessKey, now, expiresAt)
	}
	if placement == adreward.PlacementSeasonMakeup {
		return store.createSeasonMakeupSession(ctx, playerID, businessKey, now, expiresAt)
	}
	sessionID, err := newAdSessionID()
	if err != nil {
		return adreward.Session{}, err
	}
	_, err = store.db.ExecContext(ctx, `INSERT IGNORE INTO player_ad_sessions
		(session_id,player_id,placement,ad_format,business_key,state,expires_at,created_at,updated_at)
		VALUES (?, ?, ?, 'rewarded', ?, 'created', ?, ?, ?)`, sessionID, playerID, placement, businessKey, expiresAt, now, now)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("创建广告奖励会话失败: %w", err)
	}
	// SDK 加载失败或用户未播放时允许同一业务奖励在过期后重试；已经核销的业务键绝不重开。
	if _, err := store.db.ExecContext(ctx, `UPDATE player_ad_sessions SET session_id=?,expires_at=?,created_at=?,updated_at=?
		WHERE player_id=? AND placement=? AND business_key=? AND state='created' AND expires_at<=?`,
		sessionID, expiresAt, now, now, playerID, placement, businessKey, now); err != nil {
		return adreward.Session{}, fmt.Errorf("刷新过期广告奖励会话失败: %w", err)
	}
	var result adreward.Session
	var state string
	err = store.db.QueryRowContext(ctx, `SELECT session_id,placement,business_key,expires_at,state
		FROM player_ad_sessions WHERE player_id=? AND placement=? AND business_key=?`, playerID, placement, businessKey).
		Scan(&result.SessionID, &result.Placement, &result.BusinessKey, &result.ExpiresAt, &state)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("读取广告奖励会话失败: %w", err)
	}
	if !result.ExpiresAt.After(now) {
		return adreward.Session{}, adreward.ErrExpired
	}
	if state == "claimed" {
		return adreward.Session{}, adreward.ErrAlreadyClaimed
	}
	return result, nil
}

func (store *AdRewardStore) createDailyGiftSession(ctx context.Context, playerID uint64, businessKey string, now, expiresAt time.Time) (adreward.Session, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("开始创建每日礼包广告会话失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateDailyGiftAdEligibility(ctx, tx, playerID, businessKey, now, true); err != nil {
		return adreward.Session{}, err
	}
	sessionID, err := newAdSessionID()
	if err != nil {
		return adreward.Session{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO player_ad_sessions
		(session_id,player_id,placement,ad_format,business_key,state,expires_at,created_at,updated_at)
		VALUES (?, ?, 'daily_gift', 'rewarded', ?, 'created', ?, ?, ?)`,
		sessionID, playerID, businessKey, expiresAt, now, now)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("创建每日礼包广告奖励会话失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_ad_sessions SET session_id=?,expires_at=?,created_at=?,updated_at=?
		WHERE player_id=? AND placement='daily_gift' AND business_key=? AND state='created' AND expires_at<=?`,
		sessionID, expiresAt, now, now, playerID, businessKey, now); err != nil {
		return adreward.Session{}, fmt.Errorf("刷新过期每日礼包广告会话失败: %w", err)
	}
	var result adreward.Session
	var state string
	err = tx.QueryRowContext(ctx, `SELECT session_id,placement,business_key,expires_at,state
		FROM player_ad_sessions WHERE player_id=? AND placement='daily_gift' AND business_key=?`, playerID, businessKey).
		Scan(&result.SessionID, &result.Placement, &result.BusinessKey, &result.ExpiresAt, &state)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("读取每日礼包广告会话失败: %w", err)
	}
	if !result.ExpiresAt.After(now) {
		return adreward.Session{}, adreward.ErrExpired
	}
	if state == "claimed" {
		return adreward.Session{}, adreward.ErrAlreadyClaimed
	}
	if err := tx.Commit(); err != nil {
		return adreward.Session{}, fmt.Errorf("提交每日礼包广告会话失败: %w", err)
	}
	return result, nil
}

func (store *AdRewardStore) createDailyChallengeReplaySession(ctx context.Context, playerID uint64, businessKey string, now, expiresAt time.Time) (adreward.Session, error) {
	dayKey, challengeLevel, err := parseDailyChallengeBusinessKey(businessKey)
	if err != nil {
		return adreward.Session{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("开始创建每日挑战广告会话失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return adreward.Session{}, err
	}
	currentDayKey := dailychallenge.TokyoDayKey(now)
	if dayKey != currentDayKey {
		return adreward.Session{}, dailychallenge.ErrExpired
	}
	if err := ensureDailyChallengeState(ctx, tx, playerID, currentDayKey, now); err != nil {
		return adreward.Session{}, err
	}
	state, err := selectDailyChallengeState(ctx, tx, playerID, true)
	if err != nil {
		return adreward.Session{}, err
	}
	level, err := selectPlayerLevel(ctx, tx, playerID, true)
	if err != nil {
		return adreward.Session{}, err
	}
	if level < dailychallenge.UnlockLevel {
		return adreward.Session{}, dailychallenge.ErrLocked
	}
	if state.dayKey != dayKey || state.challengeLevel != challengeLevel || state.completionCount < 1 {
		return adreward.Session{}, dailychallenge.ErrStateConflict
	}
	if state.replayAvailable {
		return adreward.Session{}, dailychallenge.ErrReplayAlreadyAvailable
	}
	if state.activeAttemptID.Valid {
		return adreward.Session{}, dailychallenge.ErrStateConflict
	}
	sessionID, err := newAdSessionID()
	if err != nil {
		return adreward.Session{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO player_ad_sessions
		(session_id,player_id,placement,ad_format,business_key,state,expires_at,created_at,updated_at)
		VALUES (?,?,'daily_challenge_replay','rewarded',?,'created',?,?,?)`, sessionID, playerID, businessKey, expiresAt, now, now)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("创建每日挑战广告奖励会话失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_ad_sessions SET session_id=?,expires_at=?,created_at=?,updated_at=?
		WHERE player_id=? AND placement='daily_challenge_replay' AND business_key=? AND state='created' AND expires_at<=?`,
		sessionID, expiresAt, now, now, playerID, businessKey, now); err != nil {
		return adreward.Session{}, fmt.Errorf("刷新过期每日挑战广告会话失败: %w", err)
	}
	var result adreward.Session
	var sessionState string
	err = tx.QueryRowContext(ctx, `SELECT session_id,placement,business_key,expires_at,state FROM player_ad_sessions
		WHERE player_id=? AND placement='daily_challenge_replay' AND business_key=?`, playerID, businessKey).
		Scan(&result.SessionID, &result.Placement, &result.BusinessKey, &result.ExpiresAt, &sessionState)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("读取每日挑战广告会话失败: %w", err)
	}
	if sessionState == "claimed" {
		return adreward.Session{}, adreward.ErrAlreadyClaimed
	}
	if !result.ExpiresAt.After(now) {
		return adreward.Session{}, adreward.ErrExpired
	}
	if err := tx.Commit(); err != nil {
		return adreward.Session{}, fmt.Errorf("提交每日挑战广告会话失败: %w", err)
	}
	return result, nil
}

func (store *AdRewardStore) createSeasonMakeupSession(ctx context.Context, playerID uint64, businessKey string, now, expiresAt time.Time) (adreward.Session, error) {
	seasonKey, targetDay, err := parseSeasonMakeupBusinessKey(businessKey)
	if err != nil {
		return adreward.Session{}, err
	}
	window, err := season.WindowFor(now)
	if err != nil {
		return adreward.Session{}, err
	}
	if seasonKey != window.SeasonKey {
		return adreward.Session{}, season.ErrRewardPeriodClosed
	}
	if targetDay < 1 || targetDay > season.RewardDayCount || targetDay >= window.Day {
		return adreward.Session{}, season.ErrMakeupDayNotEligible
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("开始创建赛季补签广告会话失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return adreward.Session{}, err
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return adreward.Session{}, err
	}
	remaining, _, err := selectSeasonMakeupBalance(ctx, tx, playerID, window.SeasonKey, true)
	if err != nil {
		return adreward.Session{}, err
	}
	if remaining <= 0 {
		return adreward.Session{}, season.ErrMakeupLimitReached
	}
	targetWindow := seasonWindowForRewardDay(window, targetDay)
	if err := ensureSeasonDay(ctx, tx, playerID, targetWindow, now); err != nil {
		return adreward.Session{}, err
	}
	dayRow, err := selectSeasonDay(ctx, tx, playerID, targetWindow.DayKey, true)
	if err != nil {
		return adreward.Session{}, err
	}
	if dayRow.rewardClaimedAt.Valid {
		return adreward.Session{}, season.ErrDayAlreadyRewarded
	}
	sessionID, err := newAdSessionID()
	if err != nil {
		return adreward.Session{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO player_ad_sessions
		(session_id,player_id,placement,ad_format,business_key,state,expires_at,created_at,updated_at)
		VALUES (?,?,'season_makeup','rewarded',?,'created',?,?,?)`, sessionID, playerID, businessKey, expiresAt, now, now)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("创建赛季补签广告奖励会话失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_ad_sessions SET session_id=?,expires_at=?,created_at=?,updated_at=?
		WHERE player_id=? AND placement='season_makeup' AND business_key=? AND state='created' AND expires_at<=?`,
		sessionID, expiresAt, now, now, playerID, businessKey, now); err != nil {
		return adreward.Session{}, fmt.Errorf("刷新过期赛季补签广告会话失败: %w", err)
	}
	var result adreward.Session
	var sessionState string
	err = tx.QueryRowContext(ctx, `SELECT session_id,placement,business_key,expires_at,state FROM player_ad_sessions
		WHERE player_id=? AND placement='season_makeup' AND business_key=?`, playerID, businessKey).
		Scan(&result.SessionID, &result.Placement, &result.BusinessKey, &result.ExpiresAt, &sessionState)
	if err != nil {
		return adreward.Session{}, fmt.Errorf("读取赛季补签广告会话失败: %w", err)
	}
	if sessionState == "claimed" {
		return adreward.Session{}, adreward.ErrAlreadyClaimed
	}
	if !result.ExpiresAt.After(now) {
		return adreward.Session{}, adreward.ErrExpired
	}
	if err := tx.Commit(); err != nil {
		return adreward.Session{}, fmt.Errorf("提交赛季补签广告会话失败: %w", err)
	}
	return result, nil
}

type adSessionRow struct {
	sessionID      string
	placement      adreward.Placement
	businessKey    string
	state          string
	expiresAt      time.Time
	claimRequestID sql.NullString
	adAttemptID    sql.NullString
	claimResult    []byte
}

func (store *AdRewardStore) Claim(ctx context.Context, playerID uint64, sessionID, requestID, adAttemptID string, now time.Time) (adreward.ClaimResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return adreward.ClaimResult{}, fmt.Errorf("开始广告奖励事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row adSessionRow
	err = tx.QueryRowContext(ctx, `SELECT session_id,placement,business_key,state,expires_at,claim_request_id,ad_attempt_id,claim_result
		FROM player_ad_sessions WHERE player_id=? AND session_id=? FOR UPDATE`, playerID, sessionID).
		Scan(&row.sessionID, &row.placement, &row.businessKey, &row.state, &row.expiresAt, &row.claimRequestID, &row.adAttemptID, &row.claimResult)
	if errors.Is(err, sql.ErrNoRows) {
		return adreward.ClaimResult{}, adreward.ErrNotFound
	}
	if err != nil {
		return adreward.ClaimResult{}, fmt.Errorf("锁定广告奖励会话失败: %w", err)
	}
	if row.state == "claimed" {
		if !row.claimRequestID.Valid || row.claimRequestID.String != requestID {
			return adreward.ClaimResult{}, adreward.ErrAlreadyClaimed
		}
		if (row.adAttemptID.Valid || adAttemptID != "") && (!row.adAttemptID.Valid || row.adAttemptID.String != adAttemptID) {
			return adreward.ClaimResult{}, adreward.ErrIdempotencyKeyReused
		}
		var replay adreward.ClaimResult
		if len(row.claimResult) == 0 || json.Unmarshal(row.claimResult, &replay) != nil {
			return adreward.ClaimResult{}, fmt.Errorf("广告奖励幂等结果缺失或损坏")
		}
		return replay, nil
	}
	if !row.expiresAt.After(now) {
		return adreward.ClaimResult{}, adreward.ErrExpired
	}
	var reusedSession string
	err = tx.QueryRowContext(ctx, `SELECT session_id FROM player_ad_sessions WHERE player_id=? AND claim_request_id=? LIMIT 1`, playerID, requestID).Scan(&reusedSession)
	if err == nil && reusedSession != sessionID {
		return adreward.ClaimResult{}, adreward.ErrIdempotencyKeyReused
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return adreward.ClaimResult{}, fmt.Errorf("检查广告奖励幂等键失败: %w", err)
	}
	if (row.placement == adreward.PlacementSeasonMakeup || row.placement == adreward.PlacementPotion) && adAttemptID == "" {
		return adreward.ClaimResult{}, adreward.ErrInvalidRequest
	}
	if adAttemptID != "" {
		var reusedAttemptSession string
		err = tx.QueryRowContext(ctx, `SELECT session_id FROM player_ad_sessions WHERE player_id=? AND ad_attempt_id=? LIMIT 1`, playerID, adAttemptID).Scan(&reusedAttemptSession)
		if err == nil && reusedAttemptSession != sessionID {
			return adreward.ClaimResult{}, adreward.ErrIdempotencyKeyReused
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return adreward.ClaimResult{}, fmt.Errorf("检查广告尝试幂等键失败: %w", err)
		}
	}
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return adreward.ClaimResult{}, err
	}
	rewards, challenge, seasonState, err := grantAdReward(ctx, tx, playerID, row, adAttemptID, now)
	if err != nil {
		return adreward.ClaimResult{}, err
	}
	save, err := selectSave(ctx, tx, playerID)
	if err != nil {
		return adreward.ClaimResult{}, err
	}
	result := adreward.ClaimResult{SessionID: sessionID, Placement: row.placement, GrantedRewards: rewards, Save: save, Challenge: challenge, Season: seasonState}
	encoded, err := json.Marshal(result)
	if err != nil {
		return adreward.ClaimResult{}, fmt.Errorf("编码广告奖励结果失败: %w", err)
	}
	update, err := tx.ExecContext(ctx, `UPDATE player_ad_sessions SET state='claimed',claim_request_id=?,ad_attempt_id=?,claimed_at=?,claim_result=?,updated_at=?
		WHERE player_id=? AND session_id=? AND state='created'`, requestID, nullableString(adAttemptID), now, encoded, now, playerID, sessionID)
	if err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return adreward.ClaimResult{}, adreward.ErrIdempotencyKeyReused
		}
		return adreward.ClaimResult{}, fmt.Errorf("提交广告奖励领取状态失败: %w", err)
	}
	updated, err := affected(update)
	if err != nil {
		return adreward.ClaimResult{}, err
	}
	if !updated {
		return adreward.ClaimResult{}, adreward.ErrAlreadyClaimed
	}
	if err := tx.Commit(); err != nil {
		return adreward.ClaimResult{}, fmt.Errorf("提交广告奖励事务失败: %w", err)
	}
	return result, nil
}

func grantAdReward(ctx context.Context, tx *sql.Tx, playerID uint64, row adSessionRow, adAttemptID string, now time.Time) ([]adreward.Reward, *dailychallenge.State, *season.State, error) {
	switch row.placement {
	case adreward.PlacementPotion:
		// 魔药只核销当前广告会话；关卡资格由客户端持有，不修改库存或 revision。
		return []adreward.Reward{}, nil, nil, nil
	case adreward.PlacementHint:
		rewards, err := grantAdProp(ctx, tx, playerID, row.sessionID, player.PropTypeHint, 1, now)
		return rewards, nil, nil, err
	case adreward.PlacementShuffle:
		rewards, err := grantAdProp(ctx, tx, playerID, row.sessionID, player.PropTypeShuffle, 1, now)
		return rewards, nil, nil, err
	case adreward.PlacementAutoRemove:
		rewards, err := grantAdProp(ctx, tx, playerID, row.sessionID, player.PropTypeRemove, 1, now)
		return rewards, nil, nil, err
	case adreward.PlacementLevelComplete:
		rewards, err := grantAdTheme(ctx, tx, playerID, row, now)
		return rewards, nil, nil, err
	case adreward.PlacementDailyGift:
		// 基础礼包领取成功后，完播核销只追加发放预选的第二份奖励。
		rewards, err := grantDailyGiftBonus(ctx, tx, playerID, row.businessKey, row.sessionID, now)
		return rewards, nil, nil, err
	case adreward.PlacementDailyChallengeReplay:
		state, err := grantDailyChallengeReplay(ctx, tx, playerID, row.businessKey, now)
		return []adreward.Reward{}, &state, nil, err
	case adreward.PlacementSeasonMakeup:
		rewards, state, err := grantSeasonMakeup(ctx, tx, playerID, row, adAttemptID, now)
		return rewards, nil, &state, err
	default:
		return nil, nil, nil, adreward.ErrInvalidRequest
	}
}

func grantAdProp(ctx context.Context, tx *sql.Tx, playerID uint64, sessionID string, propType player.PropType, quantity int64, now time.Time) ([]adreward.Reward, error) {
	column := map[player.PropType]string{player.PropTypeHint: "hint_count", player.PropTypeShuffle: "shuffle_count", player.PropTypeRemove: "remove_count"}[propType]
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT `+column+` FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&count); err != nil {
		return nil, fmt.Errorf("锁定广告道具库存失败: %w", err)
	}
	if count > player.MaxPropCount-quantity {
		return nil, player.ErrPropLimitExceeded
	}
	mutationID := "ad:" + sessionID + ":prop"
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_prop_mutations
		(player_id,mutation_id,prop_type,delta,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
		playerID, mutationID, propType, quantity, adRewardReason, now); err != nil {
		return nil, fmt.Errorf("记录广告道具流水失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_saves SET revision=revision+1,`+column+`=`+column+`+?,updated_at=? WHERE player_id=?`, quantity, now, playerID); err != nil {
		return nil, fmt.Errorf("更新广告道具库存失败: %w", err)
	}
	return []adreward.Reward{{Type: "prop", PropType: propType, Quantity: quantity}}, nil
}

func grantAdTheme(ctx context.Context, tx *sql.Tx, playerID uint64, row adSessionRow, now time.Time) ([]adreward.Reward, error) {
	const prefix = "level:"
	if !strings.HasPrefix(row.businessKey, prefix) {
		return nil, adreward.ErrInvalidRequest
	}
	completedLevel, err := strconv.Atoi(strings.TrimPrefix(row.businessKey, prefix))
	if err != nil || completedLevel < player.MinLevel || completedLevel > player.MaxLevel {
		return nil, adreward.ErrInvalidRequest
	}
	var savedLevel int
	if err := tx.QueryRowContext(ctx, `SELECT level FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&savedLevel); err != nil {
		return nil, fmt.Errorf("锁定广告主题存档失败: %w", err)
	}
	if savedLevel < completedLevel+1 {
		return nil, adreward.ErrBusinessState
	}
	candidates, err := missingLimitedThemeRewards(ctx, tx, playerID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []adreward.Reward{}, nil
	}
	index, err := secureRandomIndex(len(candidates))
	if err != nil {
		return nil, err
	}
	selected := candidates[index]
	if selected.ThemeID == nil || selected.FragmentIndex == nil {
		return nil, fmt.Errorf("广告限定主题候选不完整")
	}
	mutationID := "ad:" + row.sessionID + ":theme"
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_theme_mutations
		(player_id,mutation_id,theme_id,fragment_index,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
		playerID, mutationID, *selected.ThemeID, *selected.FragmentIndex, adRewardReason, now); err != nil {
		return nil, fmt.Errorf("记录广告主题流水失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_fragments
		(player_id,theme_id,fragment_index,acquired_at) VALUES (?,?,?,?)`, playerID, *selected.ThemeID, *selected.FragmentIndex, now); err != nil {
		return nil, fmt.Errorf("写入广告主题碎片失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_saves SET revision=revision+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return nil, fmt.Errorf("更新广告主题存档失败: %w", err)
	}
	themeID, fragmentIndex := *selected.ThemeID, *selected.FragmentIndex
	return []adreward.Reward{{Type: "theme_fragment", ThemeID: &themeID, FragmentIndex: &fragmentIndex, Quantity: 1}}, nil
}

func grantDailyChallengeReplay(ctx context.Context, tx *sql.Tx, playerID uint64, businessKey string, now time.Time) (dailychallenge.State, error) {
	dayKey, challengeLevel, err := parseDailyChallengeBusinessKey(businessKey)
	if err != nil {
		return dailychallenge.State{}, err
	}
	currentDayKey := dailychallenge.TokyoDayKey(now)
	if dayKey != currentDayKey {
		return dailychallenge.State{}, dailychallenge.ErrExpired
	}
	if err := ensureDailyChallengeState(ctx, tx, playerID, currentDayKey, now); err != nil {
		return dailychallenge.State{}, err
	}
	state, err := selectDailyChallengeState(ctx, tx, playerID, true)
	if err != nil {
		return dailychallenge.State{}, err
	}
	if state.dayKey != dayKey || state.challengeLevel != challengeLevel || state.completionCount < 1 {
		return dailychallenge.State{}, dailychallenge.ErrStateConflict
	}
	if state.replayAvailable {
		return dailychallenge.State{}, dailychallenge.ErrReplayAlreadyAvailable
	}
	if state.activeAttemptID.Valid {
		return dailychallenge.State{}, dailychallenge.ErrStateConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_daily_challenge_states SET replay_available=TRUE,version=version+1,updated_at=? WHERE player_id=? AND replay_available=FALSE`, now, playerID); err != nil {
		return dailychallenge.State{}, fmt.Errorf("核销每日挑战广告资格失败: %w", err)
	}
	state.replayAvailable = true
	return buildDailyChallengeState(ctx, tx, playerID, state, now)
}

func grantSeasonMakeup(
	ctx context.Context,
	tx *sql.Tx,
	playerID uint64,
	row adSessionRow,
	adAttemptID string,
	now time.Time,
) ([]adreward.Reward, season.State, error) {
	if adAttemptID == "" {
		return nil, season.State{}, adreward.ErrInvalidRequest
	}
	seasonKey, targetDay, err := parseSeasonMakeupBusinessKey(row.businessKey)
	if err != nil {
		return nil, season.State{}, err
	}
	window, err := season.WindowFor(now)
	if err != nil {
		return nil, season.State{}, err
	}
	if seasonKey != window.SeasonKey {
		return nil, season.State{}, season.ErrRewardPeriodClosed
	}
	if targetDay < 1 || targetDay > season.RewardDayCount || targetDay >= window.Day {
		return nil, season.State{}, season.ErrMakeupDayNotEligible
	}
	// 与普通赛季领奖保持一致：在修改月份和目标日前先锁权威存档。
	var currentRevision uint64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&currentRevision); err != nil {
		return nil, season.State{}, fmt.Errorf("锁定补签权威存档失败: %w", err)
	}
	if err := ensureSeasonMonth(ctx, tx, playerID, window, now); err != nil {
		return nil, season.State{}, err
	}
	remaining, _, err := selectSeasonMakeupBalance(ctx, tx, playerID, window.SeasonKey, true)
	if err != nil {
		return nil, season.State{}, err
	}
	if remaining <= 0 {
		return nil, season.State{}, season.ErrMakeupLimitReached
	}
	targetWindow := seasonWindowForRewardDay(window, targetDay)
	if err := ensureSeasonDay(ctx, tx, playerID, targetWindow, now); err != nil {
		return nil, season.State{}, err
	}
	targetRow, err := selectSeasonDay(ctx, tx, playerID, targetWindow.DayKey, true)
	if err != nil {
		return nil, season.State{}, err
	}
	if targetRow.rewardClaimedAt.Valid {
		return nil, season.State{}, season.ErrDayAlreadyRewarded
	}
	rewards, err := decodeSeasonRewards(targetRow.rewardSnapshot, window.Config, targetDay)
	if err != nil {
		return nil, season.State{}, err
	}
	if err := grantSeasonRewards(ctx, tx, playerID, targetWindow, rewards, now); err != nil {
		return nil, season.State{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_season_days SET reward_claimed_at=?,updated_at=?
		WHERE player_id=? AND day_key=? AND reward_claimed_at IS NULL`, now, now, playerID, targetWindow.DayKey); err != nil {
		return nil, season.State{}, fmt.Errorf("标记补签日期奖励失败: %w", err)
	}
	update, err := tx.ExecContext(ctx, `UPDATE player_season_months SET makeup_remaining=makeup_remaining-1,
		state_version=state_version+1,updated_at=? WHERE player_id=? AND season_key=? AND makeup_remaining>0`,
		now, playerID, window.SeasonKey)
	if err != nil {
		return nil, season.State{}, fmt.Errorf("扣减赛季补签次数失败: %w", err)
	}
	updated, err := affected(update)
	if err != nil {
		return nil, season.State{}, err
	}
	if !updated {
		return nil, season.State{}, season.ErrMakeupLimitReached
	}
	var saveRevision uint64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM player_saves WHERE player_id=?`, playerID).Scan(&saveRevision); err != nil {
		return nil, season.State{}, fmt.Errorf("读取补签存档版本失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_season_makeup_claims
		(player_id,season_key,target_day_key,target_day,session_id,ad_attempt_id,business_key,reward_snapshot,save_revision,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, playerID, window.SeasonKey, targetWindow.DayKey, targetDay, row.sessionID,
		adAttemptID, row.businessKey, targetRow.rewardSnapshot, saveRevision, now); err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return nil, season.State{}, adreward.ErrIdempotencyKeyReused
		}
		return nil, season.State{}, fmt.Errorf("记录赛季补签审计失败: %w", err)
	}
	if window.RewardPeriodOpen {
		if err := ensureSeasonDay(ctx, tx, playerID, window, now); err != nil {
			return nil, season.State{}, err
		}
	}
	state, err := buildSeasonState(ctx, tx, playerID, window, now)
	if err != nil {
		return nil, season.State{}, err
	}
	return seasonRewardsToAdRewards(rewards), state, nil
}

func parseDailyChallengeBusinessKey(value string) (string, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "daily-challenge" {
		return "", 0, adreward.ErrInvalidRequest
	}
	parsedDay, err := time.Parse("2006-01-02", parts[1])
	if err != nil || parsedDay.Format("2006-01-02") != parts[1] {
		return "", 0, adreward.ErrInvalidRequest
	}
	challengeLevel, err := strconv.Atoi(parts[2])
	if err != nil || challengeLevel < 0 {
		return "", 0, adreward.ErrInvalidRequest
	}
	return parts[1], challengeLevel, nil
}

func parseSeasonMakeupBusinessKey(value string) (string, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "season-makeup" || len(parts[1]) != 7 {
		return "", 0, adreward.ErrInvalidRequest
	}
	parsedMonth, err := time.Parse("2006-01", parts[1])
	if err != nil || parsedMonth.Format("2006-01") != parts[1] {
		return "", 0, adreward.ErrInvalidRequest
	}
	day, err := strconv.Atoi(parts[2])
	if err != nil || strconv.Itoa(day) != parts[2] || day < 1 || day > season.RewardDayCount {
		return "", 0, adreward.ErrInvalidRequest
	}
	return parts[1], day, nil
}

func seasonRewardsToAdRewards(rewards []season.Reward) []adreward.Reward {
	result := make([]adreward.Reward, 0, len(rewards))
	for _, reward := range rewards {
		result = append(result, adreward.Reward{
			Type: reward.Type, PropType: player.PropType(reward.PropType), ThemeID: reward.ThemeID,
			FragmentIndex: reward.FragmentIndex, Quantity: reward.Quantity,
		})
	}
	return result
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func newAdSessionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成广告奖励会话失败: %w", err)
	}
	return "ad_" + hex.EncodeToString(value), nil
}

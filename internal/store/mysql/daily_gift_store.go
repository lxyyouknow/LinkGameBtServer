package mysql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"linkgame-server/internal/adreward"
	"linkgame-server/internal/dailygift"
	"linkgame-server/internal/player"
)

const (
	dailyGiftRewardVersion = 2
	dailyGiftBaseReason    = "daily_gift_base"
	dailyGiftBonusReason   = "daily_gift_bonus"
)

type DailyGiftStore struct{ db *sql.DB }

func NewDailyGiftStore(db *sql.DB) *DailyGiftStore { return &DailyGiftStore{db: db} }

type dailyGiftRow struct {
	dayKey             string
	offerID            string
	state              string
	propType           player.PropType
	propQuantity       int64
	themeID            sql.NullInt64
	fragmentIndex      sql.NullInt64
	bonusPropType      sql.NullString
	bonusPropQuantity  sql.NullInt64
	bonusThemeID       sql.NullInt64
	bonusFragmentIndex sql.NullInt64
	claimRequestID     sql.NullString
	adSessionID        sql.NullString
	claimedAt          sql.NullTime
	bonusClaimedAt     sql.NullTime
	claimResult        []byte
}

func (store *DailyGiftStore) GetOrCreate(ctx context.Context, playerID uint64, dayKey string, now time.Time) (dailygift.Gift, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return dailygift.Gift{}, fmt.Errorf("开始查询每日礼包事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return dailygift.Gift{}, err
	}
	row, err := selectDailyGiftByDay(ctx, tx, playerID, dayKey, false)
	if errors.Is(err, sql.ErrNoRows) {
		row, err = store.createDailyGift(ctx, tx, playerID, dayKey, now)
	}
	if err != nil {
		return dailygift.Gift{}, err
	}
	if err := tx.Commit(); err != nil {
		return dailygift.Gift{}, fmt.Errorf("提交每日礼包查询事务失败: %w", err)
	}
	return row.gift(), nil
}

func (store *DailyGiftStore) createDailyGift(ctx context.Context, tx *sql.Tx, playerID uint64, dayKey string, now time.Time) (dailyGiftRow, error) {
	candidates, err := missingLimitedThemeRewards(ctx, tx, playerID)
	if err != nil {
		return dailyGiftRow{}, err
	}
	// 策划尚未给出“只剩 0/1 块限定碎片”时的替代奖励，不能擅自发金币或重复碎片。
	if len(candidates) < 2 {
		return dailyGiftRow{}, dailygift.ErrInsufficientRewards
	}
	baseIndex, err := secureRandomIndex(len(candidates))
	if err != nil {
		return dailyGiftRow{}, err
	}
	base := candidates[baseIndex]
	candidates[baseIndex] = candidates[len(candidates)-1]
	candidates = candidates[:len(candidates)-1]
	bonusIndex, err := secureRandomIndex(len(candidates))
	if err != nil {
		return dailyGiftRow{}, err
	}
	bonus := candidates[bonusIndex]
	if base.ThemeID == nil || base.FragmentIndex == nil || bonus.ThemeID == nil || bonus.FragmentIndex == nil {
		return dailyGiftRow{}, fmt.Errorf("每日礼包限定主题候选不完整")
	}
	offerID, err := newDailyGiftOfferID(dayKey)
	if err != nil {
		return dailyGiftRow{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO player_daily_gifts (
		player_id, day_key, reward_version, offer_id, state,
		prop_type, prop_quantity, coin_quantity, theme_id, fragment_index,
		bonus_prop_type, bonus_prop_quantity, bonus_theme_id, bonus_fragment_index,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, 'available', 'shuffle', 1, 0, ?, ?, 'shuffle', 1, ?, ?, ?, ?)`,
		playerID, dayKey, dailyGiftRewardVersion, offerID,
		*base.ThemeID, *base.FragmentIndex, *bonus.ThemeID, *bonus.FragmentIndex, now, now)
	if err != nil {
		return dailyGiftRow{}, fmt.Errorf("创建每日礼包失败: %w", err)
	}
	row, err := selectDailyGiftByDay(ctx, tx, playerID, dayKey, true)
	if err != nil {
		return dailyGiftRow{}, fmt.Errorf("读取已创建的每日礼包失败: %w", err)
	}
	return row, nil
}

func (store *DailyGiftStore) Claim(ctx context.Context, playerID uint64, dayKey, offerID, requestID string, now time.Time) (dailygift.ClaimResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return dailygift.ClaimResult{}, fmt.Errorf("开始领取每日礼包事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := selectDailyGiftByOffer(ctx, tx, playerID, offerID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return dailygift.ClaimResult{}, dailygift.ErrNotFound
	}
	if err != nil {
		return dailygift.ClaimResult{}, err
	}
	if row.dayKey != dayKey {
		return dailygift.ClaimResult{}, dailygift.ErrExpired
	}
	if row.state == "claimed" {
		if !row.claimRequestID.Valid || row.claimRequestID.String != requestID {
			return dailygift.ClaimResult{}, dailygift.ErrAlreadyClaimed
		}
		var replay dailygift.ClaimResult
		if len(row.claimResult) == 0 || json.Unmarshal(row.claimResult, &replay) != nil {
			return dailygift.ClaimResult{}, fmt.Errorf("每日礼包幂等结果缺失或损坏")
		}
		return replay, nil
	}
	var reusedOfferID string
	err = tx.QueryRowContext(ctx, `SELECT offer_id FROM player_daily_gifts WHERE player_id=? AND claim_request_id=? LIMIT 1`, playerID, requestID).Scan(&reusedOfferID)
	if err == nil && reusedOfferID != offerID {
		return dailygift.ClaimResult{}, dailygift.ErrIdempotencyKeyReused
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dailygift.ClaimResult{}, fmt.Errorf("检查每日礼包幂等键失败: %w", err)
	}
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return dailygift.ClaimResult{}, err
	}
	baseRewards, err := grantDailyGiftStage(ctx, tx, playerID, row.offerID, "base", row.propType, row.propQuantity, row.themeID, row.fragmentIndex, now)
	if err != nil {
		return dailygift.ClaimResult{}, err
	}
	claimedAt := now.UTC()
	row.state = "claimed"
	row.claimRequestID = sql.NullString{String: requestID, Valid: true}
	row.claimedAt = sql.NullTime{Time: claimedAt, Valid: true}
	save, err := selectSave(ctx, tx, playerID)
	if err != nil {
		return dailygift.ClaimResult{}, err
	}
	result := dailygift.ClaimResult{Gift: row.gift(), GrantedRewards: baseRewards, Save: save}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return dailygift.ClaimResult{}, fmt.Errorf("编码每日礼包幂等结果失败: %w", err)
	}
	updateResult, err := tx.ExecContext(ctx, `UPDATE player_daily_gifts SET
		state='claimed', claim_request_id=?, claimed_at=?, claim_result=?, updated_at=?
	WHERE player_id=? AND day_key=? AND reward_version=? AND state='available'`,
		requestID, claimedAt, encodedResult, now, playerID, dayKey, dailyGiftRewardVersion)
	if err != nil {
		var mysqlError *drivermysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return dailygift.ClaimResult{}, dailygift.ErrIdempotencyKeyReused
		}
		return dailygift.ClaimResult{}, fmt.Errorf("提交每日礼包基础领取状态失败: %w", err)
	}
	updated, err := affected(updateResult)
	if err != nil {
		return dailygift.ClaimResult{}, err
	}
	if !updated {
		return dailygift.ClaimResult{}, dailygift.ErrAlreadyClaimed
	}
	if err := tx.Commit(); err != nil {
		return dailygift.ClaimResult{}, fmt.Errorf("提交每日礼包基础领取事务失败: %w", err)
	}
	return result, nil
}

const dailyGiftSelect = `SELECT DATE_FORMAT(day_key, '%Y-%m-%d'), offer_id, state,
	prop_type, prop_quantity, theme_id, fragment_index,
	bonus_prop_type, bonus_prop_quantity, bonus_theme_id, bonus_fragment_index,
	claim_request_id, ad_session_id, claimed_at, bonus_claimed_at, claim_result
	FROM player_daily_gifts`

func selectDailyGiftByDay(ctx context.Context, tx *sql.Tx, playerID uint64, dayKey string, forUpdate bool) (dailyGiftRow, error) {
	query := dailyGiftSelect + ` WHERE player_id=? AND day_key=? AND reward_version=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanDailyGift(tx.QueryRowContext(ctx, query, playerID, dayKey, dailyGiftRewardVersion))
}

func selectDailyGiftByOffer(ctx context.Context, tx *sql.Tx, playerID uint64, offerID string, forUpdate bool) (dailyGiftRow, error) {
	query := dailyGiftSelect + ` WHERE player_id=? AND offer_id=? AND reward_version=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	return scanDailyGift(tx.QueryRowContext(ctx, query, playerID, offerID, dailyGiftRewardVersion))
}

type rowScanner interface{ Scan(...any) error }

func scanDailyGift(scanner rowScanner) (dailyGiftRow, error) {
	var row dailyGiftRow
	if err := scanner.Scan(
		&row.dayKey, &row.offerID, &row.state,
		&row.propType, &row.propQuantity, &row.themeID, &row.fragmentIndex,
		&row.bonusPropType, &row.bonusPropQuantity, &row.bonusThemeID, &row.bonusFragmentIndex,
		&row.claimRequestID, &row.adSessionID, &row.claimedAt, &row.bonusClaimedAt, &row.claimResult,
	); err != nil {
		return dailyGiftRow{}, err
	}
	return row, nil
}

func (row dailyGiftRow) gift() dailygift.Gift {
	return dailygift.Gift{
		OfferID:        row.offerID,
		State:          row.state,
		BaseRewards:    rewardsForStage(row.propType, row.propQuantity, row.themeID, row.fragmentIndex),
		BonusRewards:   rewardsForStage(player.PropType(row.bonusPropType.String), row.bonusPropQuantity.Int64, row.bonusThemeID, row.bonusFragmentIndex),
		ClaimedAt:      nullableTime(row.claimedAt),
		BonusClaimedAt: nullableTime(row.bonusClaimedAt),
	}
}

func rewardsForStage(propType player.PropType, quantity int64, themeID, fragmentIndex sql.NullInt64) []dailygift.Reward {
	rewards := []dailygift.Reward{{Type: "prop", PropType: propType, Quantity: quantity}}
	if themeID.Valid && fragmentIndex.Valid {
		theme, fragment := int(themeID.Int64), int(fragmentIndex.Int64)
		rewards = append(rewards, dailygift.Reward{Type: "theme_fragment", ThemeID: &theme, FragmentIndex: &fragment, Quantity: 1})
	}
	return rewards
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

func validateDailyGiftAdEligibility(ctx context.Context, queryer queryContextRower, playerID uint64, offerID string, now time.Time, forUpdate bool) error {
	var dayKey, state string
	var bonusClaimedAt sql.NullTime
	query := `SELECT DATE_FORMAT(day_key, '%Y-%m-%d'),state,bonus_claimed_at
		FROM player_daily_gifts WHERE player_id=? AND offer_id=? AND reward_version=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	err := queryer.QueryRowContext(ctx, query, playerID, offerID, dailyGiftRewardVersion).
		Scan(&dayKey, &state, &bonusClaimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return adreward.ErrBusinessState
	}
	if err != nil {
		return fmt.Errorf("校验每日礼包广告资格失败: %w", err)
	}
	if dayKey != tokyoDayKey(now) || state != "claimed" || bonusClaimedAt.Valid {
		return adreward.ErrBusinessState
	}
	return nil
}

func grantDailyGiftBonus(ctx context.Context, tx *sql.Tx, playerID uint64, offerID, adSessionID string, now time.Time) ([]adreward.Reward, error) {
	row, err := selectDailyGiftByOffer(ctx, tx, playerID, offerID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, adreward.ErrBusinessState
	}
	if err != nil {
		return nil, err
	}
	if row.dayKey != tokyoDayKey(now) || row.state != "claimed" || row.bonusClaimedAt.Valid {
		return nil, adreward.ErrBusinessState
	}
	rewards, err := grantDailyGiftStage(ctx, tx, playerID, row.offerID, "bonus", player.PropType(row.bonusPropType.String), row.bonusPropQuantity.Int64, row.bonusThemeID, row.bonusFragmentIndex, now)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE player_daily_gifts SET ad_session_id=?,bonus_claimed_at=?,updated_at=?
		WHERE player_id=? AND offer_id=? AND reward_version=? AND bonus_claimed_at IS NULL`,
		adSessionID, now, now, playerID, offerID, dailyGiftRewardVersion)
	if err != nil {
		return nil, fmt.Errorf("提交每日礼包追加领取状态失败: %w", err)
	}
	updated, err := affected(result)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, adreward.ErrBusinessState
	}
	converted := make([]adreward.Reward, 0, len(rewards))
	for _, reward := range rewards {
		converted = append(converted, adreward.Reward{
			Type: reward.Type, PropType: reward.PropType, ThemeID: reward.ThemeID,
			FragmentIndex: reward.FragmentIndex, Quantity: reward.Quantity,
		})
	}
	return converted, nil
}

func grantDailyGiftStage(ctx context.Context, tx *sql.Tx, playerID uint64, offerID, stage string, propType player.PropType, propQuantity int64, themeID, fragmentIndex sql.NullInt64, now time.Time) ([]dailygift.Reward, error) {
	if propType != player.PropTypeShuffle || propQuantity != 1 || !themeID.Valid || !fragmentIndex.Valid ||
		!player.IsLimitedTheme(int(themeID.Int64)) || int(fragmentIndex.Int64) < 0 ||
		int(fragmentIndex.Int64) >= player.ThemeFragmentCount(int(themeID.Int64)) {
		return nil, dailygift.ErrInvalidRequest
	}
	var shuffleCount int64
	if err := tx.QueryRowContext(ctx, `SELECT shuffle_count FROM player_saves WHERE player_id=? FOR UPDATE`, playerID).Scan(&shuffleCount); err != nil {
		return nil, fmt.Errorf("锁定每日礼包玩家存档失败: %w", err)
	}
	if shuffleCount > player.MaxPropCount-propQuantity {
		return nil, player.ErrPropLimitExceeded
	}
	propSuffix, themeSuffix, reason := "bp", "bt", dailyGiftBaseReason
	if stage == "bonus" {
		propSuffix, themeSuffix, reason = "xp", "xt", dailyGiftBonusReason
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_prop_mutations
		(player_id,mutation_id,prop_type,delta,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
		playerID, giftMutationID(offerID, propSuffix), propType, propQuantity, reason, now); err != nil {
		return nil, fmt.Errorf("记录每日礼包道具流水失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_theme_mutations
		(player_id,mutation_id,theme_id,fragment_index,reason,client_version,created_at) VALUES (?,?,?,?,?,'',?)`,
		playerID, giftMutationID(offerID, themeSuffix), themeID.Int64, fragmentIndex.Int64, reason, now); err != nil {
		return nil, fmt.Errorf("记录每日礼包主题流水失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_fragments
		(player_id,theme_id,fragment_index,acquired_at) VALUES (?,?,?,?)`, playerID, themeID.Int64, fragmentIndex.Int64, now); err != nil {
		return nil, fmt.Errorf("写入每日礼包主题碎片失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_saves SET revision=revision+1,shuffle_count=shuffle_count+?,updated_at=? WHERE player_id=?`, propQuantity, now, playerID); err != nil {
		return nil, fmt.Errorf("更新每日礼包玩家存档失败: %w", err)
	}
	return rewardsForStage(propType, propQuantity, themeID, fragmentIndex), nil
}

func tokyoDayKey(now time.Time) string { return now.UTC().Add(9 * time.Hour).Format("2006-01-02") }

func missingLimitedThemeRewards(ctx context.Context, queryer queryContextRower, playerID uint64) ([]dailygift.Reward, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT theme_id,fragment_index FROM player_theme_fragments WHERE player_id=?`, playerID)
	if err != nil {
		return nil, fmt.Errorf("查询每日礼包主题库存失败: %w", err)
	}
	defer rows.Close()
	owned := make(map[[2]int]struct{})
	for rows.Next() {
		var themeID, fragmentIndex int
		if err := rows.Scan(&themeID, &fragmentIndex); err != nil {
			return nil, fmt.Errorf("读取每日礼包主题库存失败: %w", err)
		}
		owned[[2]int{themeID, fragmentIndex}] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历每日礼包主题库存失败: %w", err)
	}
	return limitedThemeRewardCandidates(owned), nil
}

func limitedThemeRewardCandidates(owned map[[2]int]struct{}) []dailygift.Reward {
	candidates := make([]dailygift.Reward, 0)
	for themeID := 1; themeID < player.ThemeCount; themeID++ {
		if !player.IsLimitedTheme(themeID) {
			continue
		}
		for fragmentIndex := 0; fragmentIndex < player.ThemeFragmentCount(themeID); fragmentIndex++ {
			if _, exists := owned[[2]int{themeID, fragmentIndex}]; exists {
				continue
			}
			theme, fragment := themeID, fragmentIndex
			candidates = append(candidates, dailygift.Reward{
				Type: "theme_fragment", ThemeID: &theme, FragmentIndex: &fragment, Quantity: 1,
			})
		}
	}
	return candidates
}

func secureRandomIndex(length int) (int, error) {
	if length <= 1 {
		return 0, nil
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(length)))
	if err != nil {
		return 0, fmt.Errorf("生成每日礼包随机奖励失败: %w", err)
	}
	return int(value.Int64()), nil
}

func newDailyGiftOfferID(dayKey string) (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("生成每日礼包编号失败: %w", err)
	}
	return "dg_" + strings.ReplaceAll(dayKey, "-", "") + "_" + hex.EncodeToString(randomBytes), nil
}

func giftMutationID(offerID, suffix string) string { return "daily-gift:" + offerID + ":" + suffix }

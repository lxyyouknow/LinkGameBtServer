package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"linkgame-server/internal/analytics"
)

type AnalyticsStore struct{ db *sql.DB }

func NewAnalyticsStore(db *sql.DB) *AnalyticsStore { return &AnalyticsStore{db: db} }

func (store *AnalyticsStore) IngestStartup(ctx context.Context, events []analytics.StartupEvent, receivedAt time.Time) ([]string, error) {
	accepted := make([]string, 0, len(events))
	for _, event := range events {
		eventTime, _ := time.Parse(time.RFC3339Nano, event.EventTime)
		if _, err := store.db.ExecContext(ctx, `INSERT IGNORE INTO startup_diagnostic_events
			(event_id,launch_id,stage,event_time,received_at,app_id,sdk_type,channel,client_version,platform,os,system_version,tiktok_version,sdk_version,error_code,duration_ms)
			VALUES (?,?,?,?,?,?,?,?,?, ?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?)`,
			event.ID, event.LaunchID, event.Stage, eventTime.UTC(), receivedAt, event.AppID, event.SDKType,
			event.Channel, event.ClientVersion, event.Platform, event.OS, event.System, event.TikTokVersion,
			event.SDKVersion, event.ErrorCode, event.DurationMS); err != nil {
			return nil, fmt.Errorf("写入启动诊断事件失败: %w", err)
		}
		accepted = append(accepted, event.ID)
	}
	return accepted, nil
}

func (store *AnalyticsStore) StartupDiagnostics(ctx context.Context, filter analytics.Filter) (analytics.StartupDiagnostics, error) {
	where, args := eventDateWhere("e", filter, "e.received_at")
	var result analytics.StartupDiagnostics
	if err := store.db.QueryRowContext(ctx, `SELECT
		COUNT(DISTINCT CASE WHEN e.stage='sdk_start' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='sdk_success' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='sdk_fail' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='exchange_start' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='exchange_success' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='exchange_fail' THEN e.launch_id END),
		COUNT(DISTINCT CASE WHEN e.stage='interactive_ready' THEN e.launch_id END)
		FROM startup_diagnostic_events e WHERE `+where, args...).Scan(
		&result.Summary.SDKAttempts, &result.Summary.SDKSuccesses, &result.Summary.SDKFailures,
		&result.Summary.ExchangeAttempts, &result.Summary.ExchangeSuccesses, &result.Summary.ExchangeFailures,
		&result.Summary.InteractiveReady,
	); err != nil {
		return analytics.StartupDiagnostics{}, fmt.Errorf("查询启动诊断汇总失败: %w", err)
	}
	if result.Summary.SDKAttempts > 0 {
		result.Summary.SDKSuccessRate = float64(result.Summary.SDKSuccesses) * 100 / float64(result.Summary.SDKAttempts)
		result.Summary.InteractiveRate = float64(result.Summary.InteractiveReady) * 100 / float64(result.Summary.SDKAttempts)
	}
	if result.Summary.ExchangeAttempts > 0 {
		result.Summary.ExchangeSuccessRate = float64(result.Summary.ExchangeSuccesses) * 100 / float64(result.Summary.ExchangeAttempts)
	}

	rows, err := store.db.QueryContext(ctx, `SELECT DATE_FORMAT(e.received_at,'%Y-%m-%d'),e.client_version,
		COALESCE(e.os,'unknown'),COALESCE(e.tiktok_version,'unknown'),COALESCE(e.sdk_version,'unknown'),
		e.stage,COALESCE(e.error_code,''),COUNT(*),COALESCE(AVG(e.duration_ms),0)
		FROM startup_diagnostic_events e WHERE `+where+`
		GROUP BY DATE_FORMAT(e.received_at,'%Y-%m-%d'),e.client_version,e.os,e.tiktok_version,e.sdk_version,e.stage,e.error_code
		ORDER BY DATE_FORMAT(e.received_at,'%Y-%m-%d') DESC,COUNT(*) DESC,e.client_version,e.os,e.stage,e.error_code`, args...)
	if err != nil {
		return analytics.StartupDiagnostics{}, fmt.Errorf("查询启动诊断明细失败: %w", err)
	}
	defer rows.Close()
	result.Breakdown = []analytics.StartupBreakdownItem{}
	for rows.Next() {
		var item analytics.StartupBreakdownItem
		if err := rows.Scan(&item.Date, &item.ClientVersion, &item.OS, &item.TikTokVersion, &item.SDKVersion,
			&item.Stage, &item.ErrorCode, &item.Count, &item.AverageMS); err != nil {
			return analytics.StartupDiagnostics{}, err
		}
		result.Breakdown = append(result.Breakdown, item)
	}
	return result, rows.Err()
}

func (store *AnalyticsStore) Ingest(ctx context.Context, playerID uint64, events []analytics.Event, receivedAt time.Time, statDate string) ([]string, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始统计事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	accepted := make([]string, 0, len(events))
	for _, event := range events {
		eventTime, _ := time.Parse(time.RFC3339Nano, event.EventTime)
		result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO analytics_events (player_id,event_id,event_name,event_time,received_at,app_id,sdk_type,channel,client_version,platform,game_session_id,level,duration_seconds,theme_id,prop_type,ad_format,ad_placement,ad_attempt_id,ad_error_stage,ad_error_code,ad_sub_error_code,ad_result,ad_duration_ms,ad_preloaded) VALUES (?,?,?,?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?)`,
			playerID, event.ID, event.Name, eventTime.UTC(), receivedAt, event.AppID, event.SDKType, event.Channel,
			event.ClientVersion, event.Platform, event.GameSessionID, event.Level, event.DurationSeconds, event.ThemeID,
			event.PropType, event.AdFormat, event.AdPlacement, event.AdAttemptID, event.AdFailureStage, event.AdErrorCode,
			event.AdSubErrorCode, event.AdResult, event.AdDurationMS, event.AdPreloaded)
		if err != nil {
			return nil, fmt.Errorf("写入统计事件失败: %w", err)
		}
		inserted, err := affected(result)
		if err != nil {
			return nil, err
		}
		if !inserted {
			var name analytics.EventName
			var session string
			var clientVersion, platform, adFormat, adPlacement, adAttemptID, adErrorStage sql.NullString
			var adErrorCode, adSubErrorCode, adResult sql.NullString
			var adDurationMS sql.NullInt64
			var adPreloaded sql.NullBool
			var appID, sdkType, channel int
			if err := tx.QueryRowContext(ctx, `SELECT event_name,game_session_id,app_id,sdk_type,channel,client_version,platform,ad_format,ad_placement,ad_attempt_id,ad_error_stage,ad_error_code,ad_sub_error_code,ad_result,ad_duration_ms,ad_preloaded FROM analytics_events WHERE player_id=? AND event_id=?`, playerID, event.ID).Scan(
				&name, &session, &appID, &sdkType, &channel, &clientVersion, &platform, &adFormat, &adPlacement,
				&adAttemptID, &adErrorStage, &adErrorCode, &adSubErrorCode, &adResult, &adDurationMS, &adPreloaded,
			); err != nil {
				return nil, fmt.Errorf("核对重复统计事件失败: %w", err)
			}
			if name != event.Name || session != event.GameSessionID || appID != event.AppID || sdkType != event.SDKType || channel != event.Channel ||
				clientVersion.String != event.ClientVersion || platform.String != event.Platform || adFormat.String != event.AdFormat ||
				adPlacement.String != event.AdPlacement || adAttemptID.String != event.AdAttemptID || adErrorStage.String != event.AdFailureStage ||
				adErrorCode.String != event.AdErrorCode || adSubErrorCode.String != event.AdSubErrorCode || adResult.String != event.AdResult ||
				!nullableIntMatches(adDurationMS, event.AdDurationMS) || !nullableBoolMatches(adPreloaded, event.AdPreloaded) {
				return nil, analytics.ErrInvalidEvent
			}
			accepted = append(accepted, event.ID)
			continue
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_player_stats (player_id,app_id,sdk_type,channel,first_seen_date,last_login_date,created_at,last_login_at) VALUES (?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE app_id=VALUES(app_id),sdk_type=VALUES(sdk_type),channel=VALUES(channel)`, playerID, event.AppID, event.SDKType, event.Channel, statDate, statDate, receivedAt, receivedAt); err != nil {
			return nil, fmt.Errorf("初始化玩家统计失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_daily_user_stats (stat_date,player_id,app_id,sdk_type,channel) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE app_id=VALUES(app_id),sdk_type=VALUES(sdk_type),channel=VALUES(channel)`, statDate, playerID, event.AppID, event.SDKType, event.Channel); err != nil {
			return nil, fmt.Errorf("初始化每日统计失败: %w", err)
		}

		if err := applyEventAggregate(ctx, tx, playerID, event, statDate, receivedAt); err != nil {
			return nil, err
		}
		if isFunnelEvent(event.Name) {
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO analytics_funnel_events (player_id,app_id,sdk_type,channel,step,level,first_occurred_at) VALUES (?,?,?,?,?,?,?)`, playerID, event.AppID, event.SDKType, event.Channel, event.Name, *event.Level, receivedAt); err != nil {
				return nil, fmt.Errorf("写入关卡漏斗失败: %w", err)
			}
		}
		accepted = append(accepted, event.ID)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交统计事务失败: %w", err)
	}
	return accepted, nil
}

func (store *AnalyticsStore) AdPerformance(ctx context.Context, filter analytics.Filter) ([]analytics.AdItem, error) {
	where, args := eventDateWhere("e", filter, "e.received_at")
	rows, err := store.db.QueryContext(ctx, `SELECT DATE_FORMAT(e.received_at,'%Y-%m-%d'),COALESCE(e.client_version,'legacy'),COALESCE(e.platform,'legacy'),e.ad_format,e.ad_placement,
		COUNT(DISTINCT CASE WHEN e.event_name='ad_request' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_create' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_create' AND e.ad_preloaded=TRUE THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_load' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_show' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_close' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_success' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_close' AND e.ad_result='early_closed' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_fail' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_reward_claim_success' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COUNT(DISTINCT CASE WHEN e.event_name='ad_reward_claim_fail' THEN COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id) END),
		COALESCE(AVG(CASE WHEN e.event_name='ad_load' THEN e.ad_duration_ms END),0),
		COALESCE(AVG(CASE WHEN e.event_name='ad_show' THEN e.ad_duration_ms END),0),
		COALESCE(AVG(CASE WHEN e.event_name='ad_close' THEN e.ad_duration_ms END),0)
		FROM analytics_events e WHERE `+where+` AND e.event_name IN ('ad_request','ad_create','ad_load','ad_show','ad_close','ad_success','ad_fail','ad_reward_claim_success','ad_reward_claim_fail')
		GROUP BY DATE_FORMAT(e.received_at,'%Y-%m-%d'),e.client_version,e.platform,e.ad_format,e.ad_placement ORDER BY DATE_FORMAT(e.received_at,'%Y-%m-%d'),e.client_version,e.platform,e.ad_format,e.ad_placement`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询广告统计失败: %w", err)
	}
	defer rows.Close()
	items := []analytics.AdItem{}
	for rows.Next() {
		var item analytics.AdItem
		if err := rows.Scan(
			&item.Date, &item.ClientVersion, &item.Platform, &item.Format, &item.Placement, &item.Requests, &item.Creates, &item.PreloadCreates,
			&item.Loads, &item.Shows, &item.Closes, &item.Successes, &item.EarlyCloses, &item.Failures,
			&item.RewardClaims, &item.RewardClaimFailures, &item.AverageLoadMS, &item.AverageShowMS, &item.AveragePlaybackMS,
		); err != nil {
			return nil, err
		}
		if item.Creates > 0 {
			item.FillRate = float64(item.Loads) * 100 / float64(item.Creates)
		}
		if item.Requests > 0 {
			item.ShowRate = float64(item.Shows) * 100 / float64(item.Requests)
		}
		if item.Shows > 0 {
			item.CompletionRate = float64(item.Successes) * 100 / float64(item.Shows)
		}
		if item.Successes > 0 && item.Format == "rewarded" {
			item.ClaimRate = float64(item.RewardClaims) * 100 / float64(item.Successes)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *AnalyticsStore) AdFailures(ctx context.Context, filter analytics.Filter) ([]analytics.AdFailureItem, error) {
	where, args := eventDateWhere("e", filter, "e.received_at")
	rows, err := store.db.QueryContext(ctx, `SELECT DATE_FORMAT(e.received_at,'%Y-%m-%d'),COALESCE(e.client_version,'legacy'),COALESCE(e.platform,'legacy'),e.ad_format,e.ad_placement,
		COALESCE(e.ad_error_stage,'legacy'),e.ad_error_code,COALESCE(e.ad_sub_error_code,''),COUNT(*)
		FROM analytics_events e WHERE `+where+` AND e.event_name IN ('ad_fail','ad_reward_claim_fail')
		GROUP BY DATE_FORMAT(e.received_at,'%Y-%m-%d'),e.client_version,e.platform,e.ad_format,e.ad_placement,e.ad_error_stage,e.ad_error_code,e.ad_sub_error_code
		ORDER BY DATE_FORMAT(e.received_at,'%Y-%m-%d') DESC,COUNT(*) DESC,e.client_version,e.platform,e.ad_format,e.ad_placement`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询广告失败明细失败: %w", err)
	}
	defer rows.Close()
	items := []analytics.AdFailureItem{}
	for rows.Next() {
		var item analytics.AdFailureItem
		if err := rows.Scan(&item.Date, &item.ClientVersion, &item.Platform, &item.Format, &item.Placement, &item.Stage, &item.ErrorCode, &item.SubErrorCode, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableIntMatches(stored sql.NullInt64, value *int) bool {
	if value == nil {
		return !stored.Valid
	}
	return stored.Valid && stored.Int64 == int64(*value)
}

func nullableBoolMatches(stored sql.NullBool, value *bool) bool {
	if value == nil {
		return !stored.Valid
	}
	return stored.Valid && stored.Bool == *value
}

func applyEventAggregate(ctx context.Context, tx *sql.Tx, playerID uint64, event analytics.Event, statDate string, receivedAt time.Time) error {
	playerUpdate, dailyUpdate := aggregateUpdates(playerID, event, statDate, receivedAt)
	if playerUpdate.query == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, playerUpdate.query, playerUpdate.args...); err != nil {
		return fmt.Errorf("更新玩家统计失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dailyUpdate.query, dailyUpdate.args...); err != nil {
		return fmt.Errorf("更新每日统计失败: %w", err)
	}
	return nil
}

type aggregateUpdate struct {
	query string
	args  []any
}

// aggregateUpdates 分开生成玩家累计与每日累计的参数，避免两条 SQL 共用切片时
// 因占位符数量不同造成运行时 500。
func aggregateUpdates(playerID uint64, event analytics.Event, statDate string, receivedAt time.Time) (aggregateUpdate, aggregateUpdate) {
	switch event.Name {
	case analytics.EventSessionStart:
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET login_count=login_count+1,last_login_date=?,last_login_at=? WHERE player_id=?`,
				args:  []any{statDate, receivedAt, playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET login_count=login_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{statDate, playerID},
			}
	case analytics.EventEnterGame:
		if event.ClientVersion == "" || event.ClientVersion == "0.1.0" {
			// 0.1.0 与未标记版本只在点击主线开始时上报 enter_game；过渡期同步计入主线开始。
			return aggregateUpdate{
					query: `UPDATE analytics_player_stats SET enter_game_count=enter_game_count+1,main_level_start_count=main_level_start_count+1 WHERE player_id=?`,
					args:  []any{playerID},
				}, aggregateUpdate{
					query: `UPDATE analytics_daily_user_stats SET enter_game_count=enter_game_count+1,main_level_start_count=main_level_start_count+1 WHERE stat_date=? AND player_id=?`,
					args:  []any{statDate, playerID},
				}
		}
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET enter_game_count=enter_game_count+1 WHERE player_id=?`,
				args:  []any{playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET enter_game_count=enter_game_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{statDate, playerID},
			}
	case analytics.EventMainLevelStart:
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET main_level_start_count=main_level_start_count+1 WHERE player_id=?`,
				args:  []any{playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET main_level_start_count=main_level_start_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{statDate, playerID},
			}
	case analytics.EventOnlineTime:
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET online_duration=online_duration+? WHERE player_id=?`,
				args:  []any{*event.DurationSeconds, playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET online_duration=online_duration+? WHERE stat_date=? AND player_id=?`,
				args:  []any{*event.DurationSeconds, statDate, playerID},
			}
	case analytics.EventLevelPass:
		next := *event.Level + 1
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET current_level=GREATEST(current_level,?),level_pass_count=level_pass_count+1 WHERE player_id=?`,
				args:  []any{next, playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET current_level=GREATEST(current_level,?),level_pass_count=level_pass_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{next, statDate, playerID},
			}
	case analytics.EventThemeSelect:
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET theme_select_count=theme_select_count+1 WHERE player_id=?`,
				args:  []any{playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET theme_select_count=theme_select_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{statDate, playerID},
			}
	case analytics.EventPropUse:
		return aggregateUpdate{
				query: `UPDATE analytics_player_stats SET prop_use_count=prop_use_count+1 WHERE player_id=?`,
				args:  []any{playerID},
			}, aggregateUpdate{
				query: `UPDATE analytics_daily_user_stats SET prop_use_count=prop_use_count+1 WHERE stat_date=? AND player_id=?`,
				args:  []any{statDate, playerID},
			}
	default:
		return aggregateUpdate{}, aggregateUpdate{}
	}
}

func isFunnelEvent(name analytics.EventName) bool {
	switch name {
	case analytics.EventLevelStart, analytics.EventFirstClick, analytics.EventFirstMatch, analytics.EventProgress25, analytics.EventProgress50, analytics.EventProgress75, analytics.EventLevelPass, analytics.EventNextLevel, analytics.EventRetry, analytics.EventLevelFail:
		return true
	default:
		return false
	}
}

func (store *AnalyticsStore) Overview(ctx context.Context, filter analytics.Filter) (analytics.Overview, error) {
	fromDate, toDate := dateBounds(filter)

	// 兼容旧版后台：活跃、登录和平均在线仍按所选自然日内的行为统计。
	where, args := dailyWhere("d", filter, fromDate, toDate)
	var result analytics.Overview
	var totalOnline int64
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT d.player_id),COALESCE(SUM(d.login_count),0),COUNT(DISTINCT CASE WHEN d.enter_game_count>0 THEN d.player_id END),COALESCE(SUM(d.online_duration),0) FROM analytics_daily_user_stats d WHERE `+where, args...).Scan(&result.ActivePlayers, &result.LoginCount, &result.EnteredPlayers, &totalOnline)
	if err != nil {
		return result, fmt.Errorf("查询统计总览失败: %w", err)
	}
	if result.ActivePlayers > 0 {
		result.AverageOnline = float64(totalOnline) / float64(result.ActivePlayers)
	}

	// 老板总览使用新增 cohort：日期筛选的是首次出现日期，之后看这批玩家截至当前的累计行为。
	playerWhere, playerArgs := playerDateWhere("p", filter, "p.first_seen_date", fromDate, toDate)
	var cohortOnline int64
	mainLevelEntered := mainLevelEnteredCondition("p")
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(p.login_count),0),COUNT(CASE WHEN p.enter_game_count>0 THEN 1 END),COUNT(CASE WHEN `+mainLevelEntered+` THEN 1 END),COUNT(CASE WHEN p.main_level_start_count>0 THEN 1 END),COALESCE(SUM(p.online_duration),0) FROM analytics_player_stats p WHERE `+playerWhere, playerArgs...).Scan(
		&result.NewPlayers,
		&result.CohortLoginCount,
		&result.CohortEnteredPlayers,
		&result.CohortMainLevelEnteredPlayers,
		&result.CohortMainLevelStartPlayers,
		&cohortOnline,
	); err != nil {
		return result, fmt.Errorf("查询新增玩家 cohort 失败: %w", err)
	}
	if result.NewPlayers > 0 {
		result.AverageLoginCount = float64(result.CohortLoginCount) / float64(result.NewPlayers)
		result.EnterGameRate = float64(result.CohortEnteredPlayers) * 100 / float64(result.NewPlayers)
		result.MainLevelEnterRate = float64(result.CohortMainLevelEnteredPlayers) * 100 / float64(result.NewPlayers)
		result.MainLevelStartRate = float64(result.CohortMainLevelStartPlayers) * 100 / float64(result.NewPlayers)
		result.CohortAverageOnline = float64(cohortOnline) / float64(result.NewPlayers)
	}

	// 与 HPGame 老板视图保持一致：日期筛选先固定新增 cohort，D1/D3/D7
	// 再判断这批玩家最后一次登录是否已经达到首次进入后的第 N 个自然日。
	// 这是 cohort 的后续累计表现，不是“筛选结束日当天有多少人回访”。
	day1, err := store.retention(ctx, filter, fromDate, toDate, 1)
	if err != nil {
		return result, fmt.Errorf("查询次日留存失败: %w", err)
	}
	result.Day1RetentionRate = day1.Rate
	result.Day1RetentionEligiblePlayers = day1.EligiblePlayers
	result.Day1RetainedPlayers = day1.RetainedPlayers
	result.Day1RetentionCohortDate = fromDate

	day3, err := store.retention(ctx, filter, fromDate, toDate, 3)
	if err != nil {
		return result, fmt.Errorf("查询 3 日留存失败: %w", err)
	}
	result.Day3RetentionRate = day3.Rate
	result.Day3RetentionEligiblePlayers = day3.EligiblePlayers
	result.Day3RetainedPlayers = day3.RetainedPlayers
	result.Day3RetentionCohortDate = fromDate

	day7, err := store.retention(ctx, filter, fromDate, toDate, 7)
	if err != nil {
		return result, fmt.Errorf("查询 7 日留存失败: %w", err)
	}
	result.Day7RetentionRate = day7.Rate
	result.Day7RetentionEligiblePlayers = day7.EligiblePlayers
	result.Day7RetainedPlayers = day7.RetainedPlayers
	result.Day7RetentionCohortDate = fromDate

	if err := store.overviewAds(ctx, filter, fromDate, toDate, &result); err != nil {
		return result, err
	}
	return result, nil
}

type retentionResult struct {
	EligiblePlayers int64
	RetainedPlayers int64
	Rate            float64
}

func (store *AnalyticsStore) retention(ctx context.Context, filter analytics.Filter, fromDate, toDate string, days int) (retentionResult, error) {
	query, args := retentionQuery(filter, fromDate, toDate, days)
	var eligible, retained int64
	if err := store.db.QueryRowContext(ctx, query, args...).Scan(&eligible, &retained); err != nil {
		return retentionResult{}, err
	}
	result := retentionResult{EligiblePlayers: eligible, RetainedPlayers: retained}
	if eligible == 0 {
		return result, nil
	}
	result.Rate = float64(retained) * 100 / float64(eligible)
	return result, nil
}

func retentionQuery(filter analytics.Filter, fromDate, toDate string, days int) (string, []any) {
	where, args := playerDateWhere("p", filter, "p.first_seen_date", fromDate, toDate)
	query := `SELECT COUNT(*),COALESCE(SUM(DATEDIFF(p.last_login_date,p.first_seen_date)>=?),0) FROM analytics_player_stats p WHERE ` + where
	return query, append([]any{days}, args...)
}

func (store *AnalyticsStore) OverviewTrend(ctx context.Context, filter analytics.Filter) ([]analytics.OverviewTrendItem, error) {
	fromDate, toDate := dateBounds(filter)
	where, args := playerDateWhere("p", filter, "p.first_seen_date", fromDate, toDate)
	bucketExpression, granularity, bucketArgs := overviewTrendBucket(filter, fromDate, toDate)
	queryArgs := append(bucketArgs, args...)
	mainLevelEntered := mainLevelEnteredCondition("p")
	rows, err := store.db.QueryContext(ctx, `SELECT
		`+bucketExpression+` AS cohort_bucket,COUNT(*),COALESCE(SUM(p.login_count),0),
		COUNT(CASE WHEN p.enter_game_count>0 THEN 1 END),COUNT(CASE WHEN `+mainLevelEntered+` THEN 1 END),
		COALESCE(SUM(p.online_duration),0),
		COALESCE(SUM(DATEDIFF(p.last_login_date,p.first_seen_date)>=1),0),
		COALESCE(SUM(DATEDIFF(p.last_login_date,p.first_seen_date)>=3),0),
		COALESCE(SUM(DATEDIFF(p.last_login_date,p.first_seen_date)>=7),0)
		FROM analytics_player_stats p WHERE `+where+`
		GROUP BY cohort_bucket ORDER BY cohort_bucket`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("查询经营总览 cohort 趋势失败: %w", err)
	}
	defer rows.Close()
	items := make([]analytics.OverviewTrendItem, 0)
	byDate := make(map[string]int)
	for rows.Next() {
		var item analytics.OverviewTrendItem
		var logins, entered, mainEntered, online, day1, day3, day7 int64
		if err := rows.Scan(&item.CohortDate, &item.NewPlayers, &logins, &entered, &mainEntered, &online, &day1, &day3, &day7); err != nil {
			return nil, err
		}
		item.Granularity = granularity
		if item.NewPlayers > 0 {
			item.AverageLoginCount = float64(logins) / float64(item.NewPlayers)
			item.EnterGameRate = float64(entered) * 100 / float64(item.NewPlayers)
			item.MainLevelEnterRate = float64(mainEntered) * 100 / float64(item.NewPlayers)
			item.AverageOnlineSeconds = float64(online) / float64(item.NewPlayers)
			item.Day1RetentionRate = float64(day1) * 100 / float64(item.NewPlayers)
			item.Day3RetentionRate = float64(day3) * 100 / float64(item.NewPlayers)
			item.Day7RetentionRate = float64(day7) * 100 / float64(item.NewPlayers)
		}
		items = append(items, item)
		byDate[item.CohortDate] = len(items) - 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	attempt := "CONCAT(e.player_id,':',COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id))"
	adRows, err := store.db.QueryContext(ctx, `SELECT `+bucketExpression+` AS cohort_bucket,
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_success' THEN e.player_id END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_success' THEN `+attempt+` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_reward_claim_success' THEN `+attempt+` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_success' THEN `+attempt+` END)
		FROM analytics_player_stats p
		LEFT JOIN analytics_events e ON e.player_id=p.player_id AND e.app_id=p.app_id AND e.sdk_type=p.sdk_type AND e.channel=p.channel
		WHERE `+where+` GROUP BY cohort_bucket ORDER BY cohort_bucket`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("查询经营总览广告趋势失败: %w", err)
	}
	defer adRows.Close()
	for adRows.Next() {
		var date string
		var adUsers int64
		var successes, claims, interstitialCloses int64
		if err := adRows.Scan(&date, &adUsers, &successes, &claims, &interstitialCloses); err != nil {
			return nil, err
		}
		index, ok := byDate[date]
		if !ok {
			continue
		}
		item := &items[index]
		item.RewardedAdSuccesses = successes
		item.RewardedAdRewardClaims = claims
		item.InterstitialAdNormalCloses = interstitialCloses
		if item.NewPlayers > 0 {
			item.RewardedAdWatchRate = float64(adUsers) * 100 / float64(item.NewPlayers)
			item.AverageRewardedAdSuccesses = float64(successes) / float64(item.NewPlayers)
		}
	}
	if err := adRows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func overviewTrendBucket(filter analytics.Filter, fromDate, toDate string) (string, string, []any) {
	if fromDate != toDate {
		return `DATE_FORMAT(p.first_seen_date,'%Y-%m-%d')`, "day", nil
	}
	offset := mysqlUTCOffset(filter.ReportUTCOffsetMinutes)
	return `CONCAT(DATE_FORMAT(CONVERT_TZ(p.created_at,'+00:00',?),'%H'),'时')`, "hour", []any{offset}
}

func mysqlUTCOffset(minutes int) string {
	sign := "+"
	if minutes < 0 {
		sign = "-"
		minutes = -minutes
	}
	return fmt.Sprintf("%s%02d:%02d", sign, minutes/60, minutes%60)
}

// mainLevelEnteredCondition 使用真正进入普通关卡时产生的 level_start，避免把首页按钮点击
// main_level_start 当成新玩家进入主线。新玩家会直接进入第 1 关，因此不会经过首页按钮。
func mainLevelEnteredCondition(playerAlias string) string {
	return `EXISTS (SELECT 1 FROM analytics_events main_level_event
		WHERE main_level_event.player_id=` + playerAlias + `.player_id
		AND main_level_event.app_id=` + playerAlias + `.app_id
		AND main_level_event.sdk_type=` + playerAlias + `.sdk_type
		AND main_level_event.channel=` + playerAlias + `.channel
		AND main_level_event.event_name='level_start')`
}

func (store *AnalyticsStore) overviewAds(ctx context.Context, filter analytics.Filter, fromDate, toDate string, result *analytics.Overview) error {
	playerWhere, args := playerDateWhere("p", filter, "p.first_seen_date", fromDate, toDate)
	eventClauses := []string{"e.player_id=p.player_id", "e.app_id=p.app_id", "e.sdk_type=p.sdk_type", "e.channel=p.channel"}
	attempt := "CONCAT(e.player_id,':',COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id))"
	query := `SELECT
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_success' THEN e.player_id END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_request' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_create' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_create' AND e.ad_preloaded=TRUE THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_load' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_show' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_success' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_close' AND e.ad_result='early_closed' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_fail' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='rewarded' AND e.event_name='ad_reward_claim_success' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_request' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_create' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_load' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_show' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_success' THEN ` + attempt + ` END),
		COUNT(DISTINCT CASE WHEN e.ad_format='interstitial' AND e.event_name='ad_fail' THEN ` + attempt + ` END)
		FROM analytics_player_stats p
		LEFT JOIN analytics_events e ON ` + strings.Join(eventClauses, " AND ") + `
		WHERE ` + playerWhere
	if err := store.db.QueryRowContext(ctx, query, args...).Scan(
		&result.RewardedAdWatchPlayers,
		&result.RewardedAdRequests,
		&result.RewardedAdCreates,
		&result.RewardedAdPreloadCreates,
		&result.RewardedAdLoads,
		&result.RewardedAdShows,
		&result.RewardedAdSuccesses,
		&result.RewardedAdEarlyCloses,
		&result.RewardedAdFailures,
		&result.RewardedAdRewardClaims,
		&result.InterstitialAdRequests,
		&result.InterstitialAdCreates,
		&result.InterstitialAdLoads,
		&result.InterstitialAdShows,
		&result.InterstitialAdNormalCloses,
		&result.InterstitialAdFailures,
	); err != nil {
		return fmt.Errorf("查询新增玩家广告表现失败: %w", err)
	}
	if result.NewPlayers > 0 {
		result.RewardedAdWatchRate = float64(result.RewardedAdWatchPlayers) * 100 / float64(result.NewPlayers)
		result.AverageRewardedAdSuccesses = float64(result.RewardedAdSuccesses) / float64(result.NewPlayers)
	}
	return nil
}

func (store *AnalyticsStore) Daily(ctx context.Context, filter analytics.Filter) ([]analytics.DailyItem, error) {
	fromDate, toDate := dateBounds(filter)
	where, args := dailyWhere("d", filter, fromDate, toDate)
	rows, err := store.db.QueryContext(ctx, `SELECT DATE_FORMAT(d.stat_date,'%Y-%m-%d'),COUNT(*),COALESCE(SUM(d.login_count),0),COALESCE(SUM(d.online_duration),0),COALESCE(SUM(d.level_pass_count),0) FROM analytics_daily_user_stats d WHERE `+where+` GROUP BY d.stat_date ORDER BY d.stat_date`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询每日统计失败: %w", err)
	}
	defer rows.Close()
	items := []analytics.DailyItem{}
	byDate := map[string]int{}
	for rows.Next() {
		var item analytics.DailyItem
		var online int64
		if err := rows.Scan(&item.Date, &item.ActivePlayers, &item.LoginCount, &online, &item.LevelPasses); err != nil {
			return nil, err
		}
		if item.ActivePlayers > 0 {
			item.AverageOnline = float64(online) / float64(item.ActivePlayers)
		}
		items = append(items, item)
		byDate[item.Date] = len(items) - 1
	}
	wherePlayers, argsPlayers := playerDateWhere("p", filter, "p.first_seen_date", fromDate, toDate)
	newRows, err := store.db.QueryContext(ctx, `SELECT DATE_FORMAT(p.first_seen_date,'%Y-%m-%d'),COUNT(*) FROM analytics_player_stats p WHERE `+wherePlayers+` GROUP BY p.first_seen_date`, argsPlayers...)
	if err != nil {
		return nil, err
	}
	defer newRows.Close()
	for newRows.Next() {
		var date string
		var count int64
		if err := newRows.Scan(&date, &count); err != nil {
			return nil, err
		}
		if index, exists := byDate[date]; exists {
			items[index].NewPlayers = count
		} else {
			items = append(items, analytics.DailyItem{Date: date, NewPlayers: count})
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Date < items[right].Date })
	return items, rows.Err()
}

func (store *AnalyticsStore) LevelDistribution(ctx context.Context, filter analytics.Filter) ([]analytics.LevelItem, error) {
	fromDate, toDate := dateBounds(filter)
	where, args := dailyWhere("d", filter, fromDate, toDate)
	rows, err := store.db.QueryContext(ctx, `SELECT x.current_level,COUNT(*) FROM (SELECT d.player_id,MAX(d.current_level) current_level FROM analytics_daily_user_stats d WHERE `+where+` GROUP BY d.player_id) x GROUP BY x.current_level ORDER BY x.current_level`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []analytics.LevelItem{}
	for rows.Next() {
		var i analytics.LevelItem
		if err := rows.Scan(&i.Level, &i.Players); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (store *AnalyticsStore) Funnel(ctx context.Context, filter analytics.Filter) ([]analytics.FunnelItem, error) {
	where, args := eventDateWhere("f", filter, "f.first_occurred_at")
	rows, err := store.db.QueryContext(ctx, `SELECT f.level,f.step,COUNT(*) FROM analytics_funnel_events f WHERE `+where+` GROUP BY f.level,f.step ORDER BY f.level,FIELD(f.step,'level_start','first_click','first_match','progress_25','progress_50','progress_75','level_pass','next_level','retry','level_fail')`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []analytics.FunnelItem{}
	for rows.Next() {
		var i analytics.FunnelItem
		if err := rows.Scan(&i.Level, &i.Step, &i.Users); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (store *AnalyticsStore) ThemeUsage(ctx context.Context, filter analytics.Filter) ([]analytics.CountItem, error) {
	return store.countEvents(ctx, filter, "theme_select", "CAST(theme_id AS CHAR)")
}
func (store *AnalyticsStore) PropUsage(ctx context.Context, filter analytics.Filter) ([]analytics.CountItem, error) {
	return store.countEvents(ctx, filter, "prop_use", "prop_type")
}
func (store *AnalyticsStore) countEvents(ctx context.Context, filter analytics.Filter, eventName, key string) ([]analytics.CountItem, error) {
	where, args := eventDateWhere("e", filter, "e.received_at")
	args = append([]any{eventName}, args...)
	rows, err := store.db.QueryContext(ctx, `SELECT `+key+`,COUNT(*) FROM analytics_events e WHERE e.event_name=? AND `+where+` GROUP BY `+key+` ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []analytics.CountItem{}
	for rows.Next() {
		var i analytics.CountItem
		if err := rows.Scan(&i.Key, &i.Count); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (store *AnalyticsStore) LevelRanking(ctx context.Context, filter analytics.Filter) ([]analytics.RankingItem, error) {
	fromDate, toDate := dateBounds(filter)
	query, args := levelRankingQuery(filter, fromDate, toDate)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []analytics.RankingItem{}
	for rows.Next() {
		var i analytics.RankingItem
		if err := rows.Scan(&i.PlayerID, &i.CurrentLevel, &i.LoginCount, &i.OnlineDuration, &i.CreatedAt, &i.LastLoginAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func levelRankingQuery(filter analytics.Filter, fromDate, toDate string) (string, []any) {
	where, args := playerDateWhere("s", filter, "s.first_seen_date", fromDate, toDate)
	query := `SELECT p.public_id,s.current_level,s.login_count,s.online_duration,s.created_at,s.last_login_at
		FROM analytics_player_stats s
		JOIN players p ON p.id=s.player_id
		WHERE ` + where + `
		ORDER BY s.current_level DESC,s.online_duration DESC
		LIMIT 200`
	return query, args
}

func (store *AnalyticsStore) AdRanking(ctx context.Context, filter analytics.Filter) ([]analytics.AdRankingItem, error) {
	fromDate, toDate := dateBounds(filter)
	where, args := playerDateWhere("s", filter, "s.first_seen_date", fromDate, toDate)
	eventJoin := []string{
		"e.player_id=s.player_id",
		"e.app_id=s.app_id",
		"e.sdk_type=s.sdk_type",
		"e.channel=s.channel",
		"e.event_name='ad_success'",
		"e.ad_format='rewarded'",
	}
	attempt := "CONCAT(e.player_id,':',COALESCE(NULLIF(e.ad_attempt_id,''),e.event_id))"
	rows, err := store.db.QueryContext(ctx, `SELECT p.public_id,COUNT(DISTINCT `+attempt+`),s.current_level,s.login_count,s.online_duration,s.created_at,s.last_login_at
		FROM analytics_player_stats s
		JOIN players p ON p.id=s.player_id
		JOIN analytics_events e ON `+strings.Join(eventJoin, " AND ")+`
		WHERE `+where+`
		GROUP BY s.player_id,p.public_id,s.current_level,s.login_count,s.online_duration,s.created_at,s.last_login_at
		ORDER BY COUNT(DISTINCT `+attempt+`) DESC,s.current_level DESC,s.online_duration DESC
		LIMIT 200`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询激励广告排行失败: %w", err)
	}
	defer rows.Close()
	items := []analytics.AdRankingItem{}
	for rows.Next() {
		var item analytics.AdRankingItem
		if err := rows.Scan(
			&item.PlayerID,
			&item.RewardedAdSuccesses,
			&item.CurrentLevel,
			&item.LoginCount,
			&item.OnlineDuration,
			&item.CreatedAt,
			&item.LastLoginAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func dateBounds(filter analytics.Filter) (string, string) {
	return filter.From.Format("2006-01-02"), filter.To.Add(-time.Nanosecond).Format("2006-01-02")
}
func dailyWhere(alias string, filter analytics.Filter, fromDate, toDate string) (string, []any) {
	clauses := []string{alias + `.app_id=?`, alias + `.channel=?`, alias + `.stat_date>=?`, alias + `.stat_date<=?`}
	args := []any{filter.AppID, filter.Channel, fromDate, toDate}
	if filter.SDKType != nil {
		clauses = append(clauses, alias+`.sdk_type=?`)
		args = append(args, *filter.SDKType)
	}
	return strings.Join(clauses, " AND "), args
}
func playerDateWhere(alias string, filter analytics.Filter, dateColumn, fromDate, toDate string) (string, []any) {
	clauses := []string{alias + `.app_id=?`, alias + `.channel=?`, dateColumn + `>=?`, dateColumn + `<=?`}
	args := []any{filter.AppID, filter.Channel, fromDate, toDate}
	if filter.SDKType != nil {
		clauses = append(clauses, alias+`.sdk_type=?`)
		args = append(args, *filter.SDKType)
	}
	return strings.Join(clauses, " AND "), args
}
func eventDateWhere(alias string, filter analytics.Filter, dateColumn string) (string, []any) {
	clauses := []string{alias + `.app_id=?`, alias + `.channel=?`, dateColumn + `>=?`, dateColumn + `<?`}
	args := []any{filter.AppID, filter.Channel, filter.From.UTC(), filter.To.UTC()}
	if filter.SDKType != nil {
		clauses = append(clauses, alias+`.sdk_type=?`)
		args = append(args, *filter.SDKType)
	}
	return strings.Join(clauses, " AND "), args
}

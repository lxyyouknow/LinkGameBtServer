package mysql

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"linkgame-server/internal/analytics"
	"linkgame-server/internal/player"
)

func TestAggregateUpdatesMatchSQLPlaceholders(t *testing.T) {
	duration := 30
	level := 0
	themeID := 1
	receivedAt := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
	tests := []analytics.Event{
		{Name: analytics.EventSessionStart},
		{Name: analytics.EventEnterGame, ClientVersion: "0.1.1"},
		{Name: analytics.EventMainLevelStart},
		{Name: analytics.EventOnlineTime, DurationSeconds: &duration},
		{Name: analytics.EventLevelPass, Level: &level},
		{Name: analytics.EventThemeSelect, ThemeID: &themeID},
		{Name: analytics.EventPropUse, PropType: player.PropTypeHint},
	}
	for _, event := range tests {
		t.Run(string(event.Name), func(t *testing.T) {
			playerUpdate, dailyUpdate := aggregateUpdates(42, event, "2026-08-20", receivedAt)
			for name, update := range map[string]aggregateUpdate{
				"玩家累计": playerUpdate,
				"每日累计": dailyUpdate,
			} {
				if update.query == "" {
					t.Fatalf("%s SQL 为空", name)
				}
				if placeholders := strings.Count(update.query, "?"); placeholders != len(update.args) {
					t.Fatalf("%s SQL 占位符=%d，参数=%d", name, placeholders, len(update.args))
				}
			}
		})
	}
}

func TestLegacyEnterGameAlsoCountsMainLevelStart(t *testing.T) {
	playerUpdate, dailyUpdate := aggregateUpdates(42, analytics.Event{
		Name:          analytics.EventEnterGame,
		ClientVersion: "0.1.0",
	}, "2026-09-04", time.Now())
	if !strings.Contains(playerUpdate.query, "main_level_start_count=main_level_start_count+1") ||
		!strings.Contains(dailyUpdate.query, "main_level_start_count=main_level_start_count+1") {
		t.Fatal("0.1.0 客户端的 enter_game 应兼容计入主线开始")
	}
}

func TestAggregateUpdatesIgnoreNonAggregateEvent(t *testing.T) {
	level := 0
	playerUpdate, dailyUpdate := aggregateUpdates(42, analytics.Event{
		Name:  analytics.EventLevelStart,
		Level: &level,
	}, "2026-08-20", time.Now())
	if playerUpdate.query != "" || dailyUpdate.query != "" {
		t.Fatal("level_start 不应更新累计字段")
	}
}

func TestOverview进入主线使用真实关卡启动事件(t *testing.T) {
	condition := mainLevelEnteredCondition("cohort")
	for _, expected := range []string{
		"main_level_event.event_name='level_start'",
		"main_level_event.player_id=cohort.player_id",
		"main_level_event.sdk_type=cohort.sdk_type",
	} {
		if !strings.Contains(condition, expected) {
			t.Fatalf("经营总览进入主线口径缺少 %q", expected)
		}
	}
	if strings.Contains(condition, "main_level_start") {
		t.Fatal("进入主线率不能继续使用只代表首页按钮点击的 main_level_start")
	}
}

func TestRetentionQueryUsesSelectedCohortLifetime(t *testing.T) {
	sdkType := 1000
	query, args := retentionQuery(analytics.Filter{
		AppID:   1,
		SDKType: &sdkType,
		Channel: 0,
	}, "2026-09-05", "2026-09-07", 3)
	for _, expected := range []string{
		"DATEDIFF(p.last_login_date,p.first_seen_date)>=?",
		"p.first_seen_date>=?",
		"p.first_seen_date<=?",
		"p.sdk_type=?",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("留存查询缺少 %q", expected)
		}
	}
	if strings.Contains(query, "analytics_daily_user_stats") || strings.Contains(query, "stat_date") {
		t.Fatal("HPGame cohort 留存不应按某个观察日查询每日表")
	}
	wantArgs := []any{3, 1, 0, "2026-09-05", "2026-09-07", 1000}
	if len(args) != len(wantArgs) {
		t.Fatalf("参数数量=%d，期望 %d", len(args), len(wantArgs))
	}
	for index := range wantArgs {
		if args[index] != wantArgs[index] {
			t.Fatalf("参数 %d=%v，期望 %v", index, args[index], wantArgs[index])
		}
	}
}

func TestLevelRankingQueryUsesSelectedCohortLifetime(t *testing.T) {
	sdkType := 1000
	query, args := levelRankingQuery(analytics.Filter{
		AppID:   1,
		SDKType: &sdkType,
		Channel: 0,
	}, "2026-09-07", "2026-09-07")
	for _, expected := range []string{
		"s.first_seen_date>=?",
		"s.first_seen_date<=?",
		"s.sdk_type=?",
		"s.created_at,s.last_login_at",
		"ORDER BY s.current_level DESC,s.online_duration DESC",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("闯关排行查询缺少 %q", expected)
		}
	}
	if strings.Contains(query, "analytics_daily_user_stats") || strings.Contains(query, "stat_date") {
		t.Fatal("闯关排行不应继续按所选日期活跃玩家查询每日表")
	}
	wantArgs := []any{1, 0, "2026-09-07", "2026-09-07", 1000}
	if len(args) != len(wantArgs) {
		t.Fatalf("参数数量=%d，期望 %d", len(args), len(wantArgs))
	}
	for index := range wantArgs {
		if args[index] != wantArgs[index] {
			t.Fatalf("参数 %d=%v，期望 %v", index, args[index], wantArgs[index])
		}
	}
}

func TestOverviewTrendBucketUsesHourForSingleDay(t *testing.T) {
	expression, granularity, args := overviewTrendBucket(analytics.Filter{ReportUTCOffsetMinutes: 540}, "2026-09-05", "2026-09-05")
	if granularity != "hour" || !strings.Contains(expression, "CONVERT_TZ(p.created_at,'+00:00',?)") {
		t.Fatalf("单日趋势分桶不正确: %s / %s", expression, granularity)
	}
	if len(args) != 1 || args[0] != "+09:00" {
		t.Fatalf("单日趋势时区参数=%v，期望 +09:00", args)
	}

	expression, granularity, args = overviewTrendBucket(analytics.Filter{}, "2026-09-05", "2026-09-07")
	if granularity != "day" || !strings.Contains(expression, "first_seen_date") || len(args) != 0 {
		t.Fatalf("多日趋势分桶不正确: %s / %s / %v", expression, granularity, args)
	}
}

func TestIntegrationOverviewUsesHPGameCohortLifetimeRetention(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	channel := int(time.Now().UnixNano()%1000000) + 1000000
	insertPlayer := func(index int, firstSeen, lastLogin string, loginCount, online int) uint64 {
		t.Helper()
		publicID := fmt.Sprintf("%08d-0000-4000-8000-%012d", channel, index)
		createdAt, _ := time.Parse("2006-01-02", firstSeen)
		lastLoginAt, _ := time.Parse("2006-01-02", lastLogin)
		result, err := db.ExecContext(ctx, `INSERT INTO players (public_id,status,created_at,updated_at,last_login_at) VALUES (?,'active',?,?,?)`, publicID, createdAt, createdAt, lastLoginAt)
		if err != nil {
			t.Fatalf("创建留存测试玩家失败: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		playerID := uint64(id)
		t.Cleanup(func() { cleanupAuthIntegrationPlayer(t, db, playerID) })
		if _, err := db.ExecContext(ctx, `INSERT INTO analytics_player_stats (player_id,app_id,sdk_type,channel,first_seen_date,last_login_date,created_at,last_login_at,login_count,online_duration) VALUES (?,1,1000,?,?,?,?,?,?,?)`, playerID, channel, firstSeen, lastLogin, createdAt, lastLoginAt, loginCount, online); err != nil {
			t.Fatalf("创建留存测试汇总失败: %v", err)
		}
		return playerID
	}
	insertPlayer(1, "2026-09-01", "2026-09-08", 4, 400)
	insertPlayer(2, "2026-09-02", "2026-09-04", 2, 200)

	from, _ := time.Parse("2006-01-02", "2026-09-01")
	to, _ := time.Parse("2006-01-02", "2026-09-03")
	sdkType := 1000
	filter := analytics.Filter{From: from, To: to, AppID: 1, SDKType: &sdkType, Channel: channel}
	store := NewAnalyticsStore(db)
	overview, err := store.Overview(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if overview.NewPlayers != 2 || overview.Day1RetainedPlayers != 2 || overview.Day3RetainedPlayers != 1 || overview.Day7RetainedPlayers != 1 || overview.Day1RetentionEligiblePlayers != 2 || overview.Day3RetentionEligiblePlayers != 2 || overview.Day7RetentionEligiblePlayers != 2 {
		t.Fatalf("HPGame cohort 留存不正确: %#v", overview)
	}
	items, err := store.OverviewTrend(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Day7RetentionRate != 100 || items[1].Day1RetentionRate != 100 || items[1].Day3RetentionRate != 0 {
		t.Fatalf("HPGame cohort 趋势不正确: %#v", items)
	}
}

func TestIntegrationOverviewTrendUsesFirstSeenHourForSingleDay(t *testing.T) {
	db := openAuthIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	channel := int(time.Now().UnixNano()%1000000) + 2000000
	insertPlayer := func(index int, createdAt time.Time) {
		t.Helper()
		publicID := fmt.Sprintf("%08d-0000-4000-9000-%012d", channel, index)
		result, err := db.ExecContext(ctx, `INSERT INTO players (public_id,status,created_at,updated_at,last_login_at) VALUES (?,'active',?,?,?)`, publicID, createdAt, createdAt, createdAt)
		if err != nil {
			t.Fatalf("创建小时趋势测试玩家失败: %v", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		playerID := uint64(id)
		t.Cleanup(func() { cleanupAuthIntegrationPlayer(t, db, playerID) })
		if _, err := db.ExecContext(ctx, `INSERT INTO analytics_player_stats (player_id,app_id,sdk_type,channel,first_seen_date,last_login_date,created_at,last_login_at,login_count) VALUES (?,1,1000,?,'2026-09-05','2026-09-05',?,?,1)`, playerID, channel, createdAt, createdAt); err != nil {
			t.Fatalf("创建小时趋势统计失败: %v", err)
		}
	}
	insertPlayer(1, time.Date(2026, 9, 5, 0, 15, 0, 0, time.UTC))
	insertPlayer(2, time.Date(2026, 9, 5, 3, 45, 0, 0, time.UTC))

	tokyo := time.FixedZone("Asia/Tokyo", 9*60*60)
	from := time.Date(2026, 9, 5, 0, 0, 0, 0, tokyo)
	to := from.Add(24 * time.Hour)
	sdkType := 1000
	items, err := NewAnalyticsStore(db).OverviewTrend(ctx, analytics.Filter{
		From: from, To: to, AppID: 1, SDKType: &sdkType, Channel: channel, ReportUTCOffsetMinutes: 540,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].CohortDate != "09时" || items[1].CohortDate != "12时" || items[0].Granularity != "hour" || items[1].Granularity != "hour" {
		t.Fatalf("单日小时趋势不正确: %#v", items)
	}
}

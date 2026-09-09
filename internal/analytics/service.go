// Package analytics 负责玩法与广告统计事件校验及报表查询边界。
package analytics

import (
	"context"
	"errors"
	"regexp"
	"time"

	"linkgame-server/internal/player"
)

const MaxBatchEvents = 100
const MaxStartupBatchEvents = 20

var (
	ErrInvalidEvent     = errors.New("统计事件参数不合法")
	ErrInvalidFilter    = errors.New("统计筛选参数不合法")
	eventIDPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	sessionIDPattern    = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)
	adFieldPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	versionPattern      = regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,32}$`)
	startupValuePattern = regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,64}$`)
)

type StartupStage string

const (
	StartupSDKStart         StartupStage = "sdk_start"
	StartupSDKSuccess       StartupStage = "sdk_success"
	StartupSDKFail          StartupStage = "sdk_fail"
	StartupExchangeStart    StartupStage = "exchange_start"
	StartupExchangeSuccess  StartupStage = "exchange_success"
	StartupExchangeFail     StartupStage = "exchange_fail"
	StartupInteractiveReady StartupStage = "interactive_ready"
)

// StartupEvent 不含 code、token、openid 或玩家 ID，可在登录完成前匿名上报。
type StartupEvent struct {
	ID            string       `json:"id"`
	LaunchID      string       `json:"launchId"`
	Stage         StartupStage `json:"stage"`
	EventTime     string       `json:"eventTime"`
	AppID         int          `json:"appId"`
	SDKType       int          `json:"sdkType"`
	Channel       int          `json:"channel"`
	ClientVersion string       `json:"clientVersion"`
	Platform      string       `json:"platform"`
	OS            string       `json:"os,omitempty"`
	System        string       `json:"system,omitempty"`
	TikTokVersion string       `json:"tiktokVersion,omitempty"`
	SDKVersion    string       `json:"sdkVersion,omitempty"`
	ErrorCode     string       `json:"errorCode,omitempty"`
	DurationMS    *int         `json:"durationMs,omitempty"`
}

type StartupSummary struct {
	SDKAttempts         int64   `json:"sdkAttempts"`
	SDKSuccesses        int64   `json:"sdkSuccesses"`
	SDKFailures         int64   `json:"sdkFailures"`
	SDKSuccessRate      float64 `json:"sdkSuccessRate"`
	ExchangeAttempts    int64   `json:"exchangeAttempts"`
	ExchangeSuccesses   int64   `json:"exchangeSuccesses"`
	ExchangeFailures    int64   `json:"exchangeFailures"`
	ExchangeSuccessRate float64 `json:"exchangeSuccessRate"`
	InteractiveReady    int64   `json:"interactiveReady"`
	InteractiveRate     float64 `json:"interactiveRate"`
}

type StartupBreakdownItem struct {
	Date          string  `json:"date"`
	ClientVersion string  `json:"clientVersion"`
	OS            string  `json:"os"`
	TikTokVersion string  `json:"tiktokVersion"`
	SDKVersion    string  `json:"sdkVersion"`
	Stage         string  `json:"stage"`
	ErrorCode     string  `json:"errorCode,omitempty"`
	Count         int64   `json:"count"`
	AverageMS     float64 `json:"averageMs"`
}

type StartupDiagnostics struct {
	Summary   StartupSummary         `json:"summary"`
	Breakdown []StartupBreakdownItem `json:"breakdown"`
}

type EventName string

const (
	EventSessionStart   EventName = "session_start"
	EventEnterGame      EventName = "enter_game"
	EventMainLevelStart EventName = "main_level_start"
	EventOnlineTime     EventName = "online_time"
	EventLevelStart     EventName = "level_start"
	EventFirstClick     EventName = "first_click"
	EventFirstMatch     EventName = "first_match"
	EventProgress25     EventName = "progress_25"
	EventProgress50     EventName = "progress_50"
	EventProgress75     EventName = "progress_75"
	EventLevelPass      EventName = "level_pass"
	EventNextLevel      EventName = "next_level"
	EventRetry          EventName = "retry"
	EventLevelFail      EventName = "level_fail"
	EventThemeOpen      EventName = "theme_open"
	EventThemeSelect    EventName = "theme_select"
	EventPropUse        EventName = "prop_use"
	EventAdRequest      EventName = "ad_request"
	EventAdCreate       EventName = "ad_create"
	EventAdLoad         EventName = "ad_load"
	EventAdShow         EventName = "ad_show"
	EventAdClose        EventName = "ad_close"
	EventAdSuccess      EventName = "ad_success"
	EventAdFail         EventName = "ad_fail"
	EventAdClaimOK      EventName = "ad_reward_claim_success"
	EventAdClaimFail    EventName = "ad_reward_claim_fail"
)

type Event struct {
	ID              string          `json:"id"`
	Name            EventName       `json:"name"`
	EventTime       string          `json:"eventTime"`
	AppID           int             `json:"appId"`
	SDKType         int             `json:"sdkType"`
	Channel         int             `json:"channel"`
	ClientVersion   string          `json:"clientVersion,omitempty"`
	Platform        string          `json:"platform,omitempty"`
	GameSessionID   string          `json:"gameSessionId"`
	Level           *int            `json:"level,omitempty"`
	DurationSeconds *int            `json:"durationSeconds,omitempty"`
	ThemeID         *int            `json:"themeId,omitempty"`
	PropType        player.PropType `json:"propType,omitempty"`
	AdFormat        string          `json:"adFormat,omitempty"`
	AdPlacement     string          `json:"adPlacement,omitempty"`
	AdAttemptID     string          `json:"adAttemptId,omitempty"`
	AdErrorCode     string          `json:"adErrorCode,omitempty"`
	AdSubErrorCode  string          `json:"adSubErrorCode,omitempty"`
	AdFailureStage  string          `json:"adFailureStage,omitempty"`
	AdResult        string          `json:"adResult,omitempty"`
	AdDurationMS    *int            `json:"adDurationMs,omitempty"`
	AdPreloaded     *bool           `json:"adPreloaded,omitempty"`
}

type Filter struct {
	From                   time.Time
	To                     time.Time
	AppID                  int
	SDKType                *int
	Channel                int
	ReportUTCOffsetMinutes int
}

type Overview struct {
	NewPlayers                    int64   `json:"newPlayers"`
	ActivePlayers                 int64   `json:"activePlayers"`
	LoginCount                    int64   `json:"loginCount"`
	EnteredPlayers                int64   `json:"enteredPlayers"`
	AverageOnline                 float64 `json:"averageOnlineSeconds"`
	CohortLoginCount              int64   `json:"cohortLoginCount"`
	AverageLoginCount             float64 `json:"averageLoginCount"`
	CohortEnteredPlayers          int64   `json:"cohortEnteredPlayers"`
	EnterGameRate                 float64 `json:"enterGameRate"`
	CohortMainLevelEnteredPlayers int64   `json:"cohortMainLevelEnteredPlayers"`
	MainLevelEnterRate            float64 `json:"mainLevelEnterRate"`
	CohortMainLevelStartPlayers   int64   `json:"cohortMainLevelStartPlayers"`
	MainLevelStartRate            float64 `json:"mainLevelStartRate"`
	CohortAverageOnline           float64 `json:"cohortAverageOnlineSeconds"`
	Day1RetentionRate             float64 `json:"day1RetentionRate"`
	Day1RetentionEligiblePlayers  int64   `json:"day1RetentionEligiblePlayers"`
	Day1RetainedPlayers           int64   `json:"day1RetainedPlayers"`
	Day1RetentionCohortDate       string  `json:"day1RetentionCohortDate"`
	Day3RetentionRate             float64 `json:"day3RetentionRate"`
	Day3RetentionEligiblePlayers  int64   `json:"day3RetentionEligiblePlayers"`
	Day3RetainedPlayers           int64   `json:"day3RetainedPlayers"`
	Day3RetentionCohortDate       string  `json:"day3RetentionCohortDate"`
	Day7RetentionRate             float64 `json:"day7RetentionRate"`
	Day7RetentionEligiblePlayers  int64   `json:"day7RetentionEligiblePlayers"`
	Day7RetainedPlayers           int64   `json:"day7RetainedPlayers"`
	Day7RetentionCohortDate       string  `json:"day7RetentionCohortDate"`
	RetentionObservationDate      string  `json:"retentionObservationDate"`
	RewardedAdWatchPlayers        int64   `json:"rewardedAdWatchPlayers"`
	RewardedAdWatchRate           float64 `json:"rewardedAdWatchRate"`
	RewardedAdRequests            int64   `json:"rewardedAdRequests"`
	RewardedAdCreates             int64   `json:"rewardedAdCreates"`
	RewardedAdPreloadCreates      int64   `json:"rewardedAdPreloadCreates"`
	RewardedAdLoads               int64   `json:"rewardedAdLoads"`
	RewardedAdShows               int64   `json:"rewardedAdShows"`
	RewardedAdSuccesses           int64   `json:"rewardedAdSuccesses"`
	RewardedAdEarlyCloses         int64   `json:"rewardedAdEarlyCloses"`
	RewardedAdFailures            int64   `json:"rewardedAdFailures"`
	RewardedAdRewardClaims        int64   `json:"rewardedAdRewardClaims"`
	AverageRewardedAdSuccesses    float64 `json:"averageRewardedAdSuccesses"`
	InterstitialAdRequests        int64   `json:"interstitialAdRequests"`
	InterstitialAdCreates         int64   `json:"interstitialAdCreates"`
	InterstitialAdLoads           int64   `json:"interstitialAdLoads"`
	InterstitialAdShows           int64   `json:"interstitialAdShows"`
	InterstitialAdNormalCloses    int64   `json:"interstitialAdNormalCloses"`
	InterstitialAdFailures        int64   `json:"interstitialAdFailures"`
}

// OverviewTrendItem 按新增玩家首次进入日期或小时拆分 cohort，所有行为指标均读取该批玩家截至当前的累计表现。
// 单日筛选按首次进入小时展示，多日筛选按首次进入日期展示。
type OverviewTrendItem struct {
	CohortDate                 string  `json:"cohortDate"`
	Granularity                string  `json:"granularity"`
	NewPlayers                 int64   `json:"newPlayers"`
	AverageLoginCount          float64 `json:"averageLoginCount"`
	EnterGameRate              float64 `json:"enterGameRate"`
	MainLevelEnterRate         float64 `json:"mainLevelEnterRate"`
	AverageOnlineSeconds       float64 `json:"averageOnlineSeconds"`
	Day1RetentionRate          float64 `json:"day1RetentionRate"`
	Day3RetentionRate          float64 `json:"day3RetentionRate"`
	Day7RetentionRate          float64 `json:"day7RetentionRate"`
	RewardedAdWatchRate        float64 `json:"rewardedAdWatchRate"`
	RewardedAdSuccesses        int64   `json:"rewardedAdSuccesses"`
	AverageRewardedAdSuccesses float64 `json:"averageRewardedAdSuccesses"`
	RewardedAdRewardClaims     int64   `json:"rewardedAdRewardClaims"`
	InterstitialAdNormalCloses int64   `json:"interstitialAdNormalCloses"`
}

type DailyItem struct {
	Date          string  `json:"date"`
	NewPlayers    int64   `json:"newPlayers"`
	ActivePlayers int64   `json:"activePlayers"`
	LoginCount    int64   `json:"loginCount"`
	AverageOnline float64 `json:"averageOnlineSeconds"`
	LevelPasses   int64   `json:"levelPasses"`
}

type CountItem struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type FunnelItem struct {
	Level int       `json:"level"`
	Step  EventName `json:"step"`
	Users int64     `json:"users"`
}

type LevelItem struct {
	Level   int   `json:"level"`
	Players int64 `json:"players"`
}

type AdItem struct {
	Date                string  `json:"date"`
	ClientVersion       string  `json:"clientVersion"`
	Platform            string  `json:"platform"`
	Format              string  `json:"format"`
	Placement           string  `json:"placement"`
	Requests            int64   `json:"requests"`
	Creates             int64   `json:"creates"`
	PreloadCreates      int64   `json:"preloadCreates"`
	Loads               int64   `json:"loads"`
	Shows               int64   `json:"shows"`
	Closes              int64   `json:"closes"`
	Successes           int64   `json:"successes"`
	EarlyCloses         int64   `json:"earlyCloses"`
	Failures            int64   `json:"failures"`
	RewardClaims        int64   `json:"rewardClaims"`
	RewardClaimFailures int64   `json:"rewardClaimFailures"`
	AverageLoadMS       float64 `json:"averageLoadMs"`
	AverageShowMS       float64 `json:"averageShowMs"`
	AveragePlaybackMS   float64 `json:"averagePlaybackMs"`
	FillRate            float64 `json:"fillRate"`
	ShowRate            float64 `json:"showRate"`
	CompletionRate      float64 `json:"completionRate"`
	ClaimRate           float64 `json:"claimRate"`
}

type AdFailureItem struct {
	Date          string `json:"date"`
	ClientVersion string `json:"clientVersion"`
	Platform      string `json:"platform"`
	Format        string `json:"format"`
	Placement     string `json:"placement"`
	Stage         string `json:"stage"`
	ErrorCode     string `json:"errorCode"`
	SubErrorCode  string `json:"subErrorCode,omitempty"`
	Count         int64  `json:"count"`
}

type RankingItem struct {
	PlayerID       string    `json:"playerId"`
	CurrentLevel   int       `json:"currentLevel"`
	LoginCount     int64     `json:"loginCount"`
	OnlineDuration int64     `json:"onlineDuration"`
	CreatedAt      time.Time `json:"createdAt"`
	LastLoginAt    time.Time `json:"lastLoginAt"`
}

type AdRankingItem struct {
	PlayerID            string    `json:"playerId"`
	RewardedAdSuccesses int64     `json:"rewardedAdSuccesses"`
	CurrentLevel        int       `json:"currentLevel"`
	LoginCount          int64     `json:"loginCount"`
	OnlineDuration      int64     `json:"onlineDuration"`
	CreatedAt           time.Time `json:"createdAt"`
	LastLoginAt         time.Time `json:"lastLoginAt"`
}

type Store interface {
	Ingest(ctx context.Context, playerID uint64, events []Event, receivedAt time.Time, statDate string) ([]string, error)
	IngestStartup(ctx context.Context, events []StartupEvent, receivedAt time.Time) ([]string, error)
	StartupDiagnostics(ctx context.Context, filter Filter) (StartupDiagnostics, error)
	Overview(ctx context.Context, filter Filter) (Overview, error)
	OverviewTrend(ctx context.Context, filter Filter) ([]OverviewTrendItem, error)
	Daily(ctx context.Context, filter Filter) ([]DailyItem, error)
	LevelDistribution(ctx context.Context, filter Filter) ([]LevelItem, error)
	Funnel(ctx context.Context, filter Filter) ([]FunnelItem, error)
	ThemeUsage(ctx context.Context, filter Filter) ([]CountItem, error)
	PropUsage(ctx context.Context, filter Filter) ([]CountItem, error)
	LevelRanking(ctx context.Context, filter Filter) ([]RankingItem, error)
	AdRanking(ctx context.Context, filter Filter) ([]AdRankingItem, error)
	AdPerformance(ctx context.Context, filter Filter) ([]AdItem, error)
	AdFailures(ctx context.Context, filter Filter) ([]AdFailureItem, error)
}

type Service struct {
	store    Store
	location *time.Location
	now      func() time.Time
}

func NewService(store Store, location *time.Location) *Service {
	return &Service{store: store, location: location, now: time.Now}
}

func (service *Service) Timezone() string { return service.location.String() }

func (service *Service) Ingest(ctx context.Context, playerID uint64, events []Event) ([]string, error) {
	if len(events) < 1 || len(events) > MaxBatchEvents {
		return nil, ErrInvalidEvent
	}
	for _, event := range events {
		if err := validateEvent(event); err != nil {
			return nil, err
		}
	}
	now := service.now().UTC()
	return service.store.Ingest(ctx, playerID, events, now, now.In(service.location).Format("2006-01-02"))
}

func (service *Service) IngestStartup(ctx context.Context, events []StartupEvent) ([]string, error) {
	if len(events) < 1 || len(events) > MaxStartupBatchEvents {
		return nil, ErrInvalidEvent
	}
	for _, event := range events {
		if err := validateStartupEvent(event); err != nil {
			return nil, err
		}
	}
	return service.store.IngestStartup(ctx, events, service.now().UTC())
}

func (service *Service) StartupDiagnostics(ctx context.Context, filter Filter) (StartupDiagnostics, error) {
	if !validFilter(filter) {
		return StartupDiagnostics{}, ErrInvalidFilter
	}
	return service.store.StartupDiagnostics(ctx, filter)
}

func (service *Service) Overview(ctx context.Context, filter Filter) (Overview, error) {
	if !validFilter(filter) {
		return Overview{}, ErrInvalidFilter
	}
	return service.store.Overview(ctx, filter)
}
func (service *Service) OverviewTrend(ctx context.Context, filter Filter) ([]OverviewTrendItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	_, offsetSeconds := filter.From.Zone()
	filter.ReportUTCOffsetMinutes = offsetSeconds / 60
	return service.store.OverviewTrend(ctx, filter)
}
func (service *Service) Daily(ctx context.Context, filter Filter) ([]DailyItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.Daily(ctx, filter)
}
func (service *Service) LevelDistribution(ctx context.Context, filter Filter) ([]LevelItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.LevelDistribution(ctx, filter)
}
func (service *Service) Funnel(ctx context.Context, filter Filter) ([]FunnelItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.Funnel(ctx, filter)
}
func (service *Service) ThemeUsage(ctx context.Context, filter Filter) ([]CountItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.ThemeUsage(ctx, filter)
}
func (service *Service) PropUsage(ctx context.Context, filter Filter) ([]CountItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.PropUsage(ctx, filter)
}
func (service *Service) LevelRanking(ctx context.Context, filter Filter) ([]RankingItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.LevelRanking(ctx, filter)
}
func (service *Service) AdRanking(ctx context.Context, filter Filter) ([]AdRankingItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.AdRanking(ctx, filter)
}
func (service *Service) AdPerformance(ctx context.Context, filter Filter) ([]AdItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.AdPerformance(ctx, filter)
}

func (service *Service) AdFailures(ctx context.Context, filter Filter) ([]AdFailureItem, error) {
	if !validFilter(filter) {
		return nil, ErrInvalidFilter
	}
	return service.store.AdFailures(ctx, filter)
}

func validateEvent(event Event) error {
	if !eventIDPattern.MatchString(event.ID) || !sessionIDPattern.MatchString(event.GameSessionID) ||
		event.AppID != 1 || (event.SDKType != 0 && event.SDKType != 1000) || event.Channel < 0 || event.Channel > 1_000_000 {
		return ErrInvalidEvent
	}
	if _, err := time.Parse(time.RFC3339Nano, event.EventTime); err != nil {
		return ErrInvalidEvent
	}
	if event.ClientVersion != "" && !versionPattern.MatchString(event.ClientVersion) {
		return ErrInvalidEvent
	}
	if event.Platform != "" && event.Platform != "web" && event.Platform != "tiktok" {
		return ErrInvalidEvent
	}
	if (event.Platform == "tiktok" && event.SDKType != 1000) || (event.Platform == "web" && event.SDKType != 0) {
		return ErrInvalidEvent
	}
	levelRequired := event.Name == EventLevelStart || event.Name == EventFirstClick || event.Name == EventFirstMatch ||
		event.Name == EventProgress25 || event.Name == EventProgress50 || event.Name == EventProgress75 ||
		event.Name == EventLevelPass || event.Name == EventNextLevel || event.Name == EventRetry || event.Name == EventLevelFail
	if levelRequired {
		if event.Level == nil || *event.Level < player.MinLevel || *event.Level > player.MaxLevel {
			return ErrInvalidEvent
		}
	} else if event.Level != nil {
		return ErrInvalidEvent
	}
	if event.Name == EventOnlineTime {
		if event.DurationSeconds == nil || *event.DurationSeconds < 1 || *event.DurationSeconds > 300 {
			return ErrInvalidEvent
		}
	} else if event.DurationSeconds != nil {
		return ErrInvalidEvent
	}
	if event.Name == EventThemeSelect {
		if event.ThemeID == nil || !player.ValidThemeID(*event.ThemeID) {
			return ErrInvalidEvent
		}
	} else if event.ThemeID != nil {
		return ErrInvalidEvent
	}
	if event.Name == EventPropUse {
		if event.PropType != player.PropTypeHint && event.PropType != player.PropTypeShuffle && event.PropType != player.PropTypeRemove {
			return ErrInvalidEvent
		}
	} else if event.PropType != "" {
		return ErrInvalidEvent
	}
	isAdEvent := event.Name == EventAdRequest || event.Name == EventAdCreate || event.Name == EventAdLoad ||
		event.Name == EventAdShow || event.Name == EventAdClose || event.Name == EventAdSuccess ||
		event.Name == EventAdFail || event.Name == EventAdClaimOK || event.Name == EventAdClaimFail
	if isAdEvent {
		if (event.AdFormat != "rewarded" && event.AdFormat != "interstitial") ||
			!adFieldPattern.MatchString(event.AdPlacement) {
			return ErrInvalidEvent
		}
		legacyAttemptOptional := event.Name == EventAdCreate || event.Name == EventAdSuccess || event.Name == EventAdFail
		if (event.AdAttemptID == "" && !legacyAttemptOptional) ||
			(event.AdAttemptID != "" && !adFieldPattern.MatchString(event.AdAttemptID)) {
			return ErrInvalidEvent
		}
		isFailure := event.Name == EventAdFail || event.Name == EventAdClaimFail
		if isFailure {
			if !adFieldPattern.MatchString(event.AdErrorCode) {
				return ErrInvalidEvent
			}
			if event.AdFailureStage != "" && !validAdFailureStage(event.AdFailureStage) {
				return ErrInvalidEvent
			}
			if event.AdAttemptID != "" && event.AdFailureStage == "" {
				return ErrInvalidEvent
			}
			if event.Name == EventAdClaimFail && event.AdFailureStage != "reward_claim" {
				return ErrInvalidEvent
			}
			if event.Name == EventAdFail && event.AdFailureStage == "reward_claim" {
				return ErrInvalidEvent
			}
		} else if event.AdErrorCode != "" || event.AdSubErrorCode != "" || event.AdFailureStage != "" {
			return ErrInvalidEvent
		}
		if event.AdSubErrorCode != "" && !adFieldPattern.MatchString(event.AdSubErrorCode) {
			return ErrInvalidEvent
		}
		if event.Name == EventAdClose {
			if event.AdResult != "completed" && event.AdResult != "early_closed" && event.AdResult != "closed" {
				return ErrInvalidEvent
			}
			if (event.AdFormat == "rewarded" && event.AdResult == "closed") ||
				(event.AdFormat == "interstitial" && event.AdResult != "closed") {
				return ErrInvalidEvent
			}
		} else if event.AdResult != "" {
			return ErrInvalidEvent
		}
		if event.AdDurationMS != nil && (*event.AdDurationMS < 0 || *event.AdDurationMS > 30*60*1000) {
			return ErrInvalidEvent
		}
		if (event.Name == EventAdClaimOK || event.Name == EventAdClaimFail) && event.AdFormat != "rewarded" {
			return ErrInvalidEvent
		}
	} else if event.AdFormat != "" || event.AdPlacement != "" || event.AdAttemptID != "" || event.AdErrorCode != "" ||
		event.AdSubErrorCode != "" || event.AdFailureStage != "" || event.AdResult != "" ||
		event.AdDurationMS != nil || event.AdPreloaded != nil {
		return ErrInvalidEvent
	}
	valid := event.Name == EventSessionStart || event.Name == EventEnterGame || event.Name == EventMainLevelStart || event.Name == EventOnlineTime || levelRequired ||
		event.Name == EventThemeOpen || event.Name == EventThemeSelect || event.Name == EventPropUse || isAdEvent
	if !valid {
		return ErrInvalidEvent
	}
	return nil
}

func validateStartupEvent(event StartupEvent) error {
	if !eventIDPattern.MatchString(event.ID) || !sessionIDPattern.MatchString(event.LaunchID) ||
		event.AppID != 1 || event.SDKType != 1000 || event.Channel < 0 || event.Channel > 1_000_000 ||
		event.Platform != "tiktok" || !versionPattern.MatchString(event.ClientVersion) {
		return ErrInvalidEvent
	}
	if _, err := time.Parse(time.RFC3339Nano, event.EventTime); err != nil {
		return ErrInvalidEvent
	}
	switch event.Stage {
	case StartupSDKStart, StartupSDKSuccess, StartupSDKFail, StartupExchangeStart,
		StartupExchangeSuccess, StartupExchangeFail, StartupInteractiveReady:
	default:
		return ErrInvalidEvent
	}
	if event.OS != "" && event.OS != "ios" && event.OS != "android" && event.OS != "unknown" {
		return ErrInvalidEvent
	}
	for _, value := range []string{event.System, event.TikTokVersion, event.SDKVersion, event.ErrorCode} {
		if value != "" && !startupValuePattern.MatchString(value) {
			return ErrInvalidEvent
		}
	}
	if event.DurationMS != nil && (*event.DurationMS < 0 || *event.DurationMS > 300_000) {
		return ErrInvalidEvent
	}
	return nil
}

func validAdFailureStage(stage string) bool {
	switch stage {
	case "capability", "create", "load", "show", "play", "close", "reward_claim":
		return true
	default:
		return false
	}
}

func validFilter(filter Filter) bool {
	if filter.From.IsZero() || filter.To.IsZero() || filter.To.Before(filter.From) || filter.To.Sub(filter.From) > 366*24*time.Hour || filter.AppID != 1 || filter.Channel < 0 {
		return false
	}
	return filter.SDKType == nil || *filter.SDKType == 0 || *filter.SDKType == 1000
}

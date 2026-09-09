// Package season 负责自然月赛季的权威任务、幂等通关计数与原子奖励领取。
package season

import (
	"context"
	"errors"
	"regexp"
	"time"

	"linkgame-server/internal/player"
)

const (
	RewardDayCount   = 25
	ClearLevelTarget = 5
	MaxMakeupCount   = 5
)

var (
	ErrInvalidRequest       = errors.New("赛季请求不合法")
	ErrDayChanged           = errors.New("赛季日期已经变化")
	ErrRewardPeriodClosed   = errors.New("赛季奖励期已经结束")
	ErrTaskNotReady         = errors.New("赛季任务尚未完成")
	ErrTaskAlreadyClaimed   = errors.New("赛季任务已经领取")
	ErrDayAlreadyRewarded   = errors.New("赛季当天奖励已经发放")
	ErrConfigUnavailable    = errors.New("赛季配置不可用")
	ErrRequestIDConflict    = errors.New("赛季 requestId 已用于其他参数")
	ErrStateConflict        = errors.New("赛季状态冲突")
	ErrPlayerNotFound       = errors.New("赛季玩家不存在")
	ErrMakeupLimitReached   = errors.New("赛季补签次数已用完")
	ErrMakeupDayNotEligible = errors.New("赛季补签日期不满足条件")
	identifierPattern       = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
)

type Task string

const (
	TaskLogin       Task = "login"
	TaskClearLevels Task = "clear_levels"
)

type Reward struct {
	Type          string `json:"type"`
	PropType      string `json:"propType,omitempty"`
	ThemeID       *int   `json:"themeId,omitempty"`
	FragmentIndex *int   `json:"fragmentIndex,omitempty"`
	Quantity      int64  `json:"quantity"`
}

type RewardDay struct {
	Day     int      `json:"day"`
	Rewards []Reward `json:"rewards"`
}

type TaskProgress struct {
	Target    int        `json:"target"`
	Progress  int        `json:"progress"`
	Claimable bool       `json:"claimable"`
	ClaimedAt *time.Time `json:"claimedAt"`
}

type TodayState struct {
	DayKey          string       `json:"dayKey"`
	Day             int          `json:"day"`
	ClearedLevels   int          `json:"clearedLevels"`
	Login           TaskProgress `json:"login"`
	ClearLevels     TaskProgress `json:"clearLevels"`
	RewardClaimedAt *time.Time   `json:"rewardClaimedAt"`
}

type State struct {
	ServerTime       time.Time   `json:"serverTime"`
	Timezone         string      `json:"timezone"`
	SeasonKey        string      `json:"seasonKey"`
	ConfigVersion    string      `json:"configVersion"`
	ConfigID         int         `json:"configId"`
	ThemeID          int         `json:"themeId"`
	StartsAt         time.Time   `json:"startsAt"`
	EndsAt           time.Time   `json:"endsAt"`
	NextResetAt      time.Time   `json:"nextResetAt"`
	CurrentRewardDay *int        `json:"currentRewardDay"`
	StateVersion     uint64      `json:"stateVersion"`
	ClaimedDays      []int       `json:"claimedDays"`
	MakeupRemaining  int         `json:"makeupRemaining"`
	Today            *TodayState `json:"today"`
	RewardDays       []RewardDay `json:"rewardDays"`
}

type ClaimState struct {
	SeasonKey       string     `json:"seasonKey"`
	DayKey          string     `json:"dayKey"`
	StateVersion    uint64     `json:"stateVersion"`
	ClearedLevels   int        `json:"clearedLevels"`
	LoginClaimedAt  *time.Time `json:"loginClaimedAt"`
	ClearClaimedAt  *time.Time `json:"clearClaimedAt"`
	RewardClaimedAt *time.Time `json:"rewardClaimedAt"`
	ClaimedDays     []int      `json:"claimedDays"`
}

type ClaimResult struct {
	AcceptedTask   Task        `json:"acceptedTask"`
	GrantedRewards []Reward    `json:"grantedRewards"`
	Season         ClaimState  `json:"season"`
	Save           player.Save `json:"save"`
}

type ProgressResult struct {
	SeasonKey     string `json:"seasonKey"`
	DayKey        string `json:"dayKey"`
	StateVersion  uint64 `json:"stateVersion"`
	ClearedLevels int    `json:"clearedLevels"`
	Claimable     bool   `json:"claimable"`
}

type AdminClearEvent struct {
	RequestID    string    `json:"requestId"`
	CompletionID string    `json:"completionId"`
	Source       string    `json:"source"`
	Level        *int      `json:"level"`
	DayKey       string    `json:"dayKey"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AdminClaim struct {
	RequestID string      `json:"requestId"`
	Task      Task        `json:"task"`
	DayKey    string      `json:"dayKey"`
	Result    ClaimResult `json:"result"`
	CreatedAt time.Time   `json:"createdAt"`
}

type AdminMakeupClaim struct {
	TargetDay      int       `json:"targetDay"`
	TargetDayKey   string    `json:"targetDayKey"`
	SessionID      string    `json:"sessionId"`
	AdAttemptID    string    `json:"adAttemptId"`
	BusinessKey    string    `json:"businessKey"`
	RewardSnapshot []Reward  `json:"rewardSnapshot"`
	SaveRevision   uint64    `json:"saveRevision"`
	CreatedAt      time.Time `json:"createdAt"`
}

type AdminAudit struct {
	PlayerID             string             `json:"playerId"`
	State                State              `json:"state"`
	MakeupRecoveryDayKey string             `json:"makeupRecoveryDayKey"`
	ClearEvents          []AdminClearEvent  `json:"clearEvents"`
	Claims               []AdminClaim       `json:"claims"`
	MakeupClaims         []AdminMakeupClaim `json:"makeupClaims"`
	RewardMutations      []string           `json:"rewardMutations"`
}

type Window struct {
	SeasonKey        string
	DayKey           string
	Day              int
	Config           Config
	StartsAt         time.Time
	EndsAt           time.Time
	NextResetAt      time.Time
	RewardPeriodOpen bool
}

type Store interface {
	Get(context.Context, uint64, Window, time.Time) (State, error)
	RecordClear(context.Context, uint64, string, string, int, Window, time.Time) (ProgressResult, error)
	Claim(context.Context, uint64, Task, string, string, string, Window, time.Time) (ClaimResult, error)
	InspectPlayer(context.Context, string, Window, time.Time) (AdminAudit, error)
}

func (service *Service) InspectPlayer(ctx context.Context, publicPlayerID string) (AdminAudit, error) {
	if service == nil || service.store == nil || !publicPlayerIDPattern.MatchString(publicPlayerID) {
		return AdminAudit{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	window, err := WindowFor(now)
	if err != nil {
		return AdminAudit{}, err
	}
	return service.store.InspectPlayer(ctx, publicPlayerID, window, now)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

func (service *Service) Get(ctx context.Context, playerID uint64) (State, error) {
	if service == nil || service.store == nil || playerID == 0 {
		return State{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	window, err := WindowFor(now)
	if err != nil {
		return State{}, err
	}
	return service.store.Get(ctx, playerID, window, now)
}

func (service *Service) RecordLevelClear(
	ctx context.Context,
	playerID uint64,
	requestID string,
	completionID string,
	level int,
) (ProgressResult, error) {
	if service == nil || service.store == nil || playerID == 0 ||
		!identifierPattern.MatchString(requestID) || !identifierPattern.MatchString(completionID) ||
		level < player.MinLevel || level > player.MaxLevel {
		return ProgressResult{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	window, err := WindowFor(now)
	if err != nil {
		return ProgressResult{}, err
	}
	return service.store.RecordClear(ctx, playerID, requestID, completionID, level, window, now)
}

func (service *Service) Claim(
	ctx context.Context,
	playerID uint64,
	task Task,
	requestID string,
	expectedSeasonKey string,
	expectedDayKey string,
) (ClaimResult, error) {
	if service == nil || service.store == nil || playerID == 0 ||
		(task != TaskLogin && task != TaskClearLevels) || !identifierPattern.MatchString(requestID) ||
		!seasonKeyPattern.MatchString(expectedSeasonKey) || !dayKeyPattern.MatchString(expectedDayKey) {
		return ClaimResult{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	window, err := WindowFor(now)
	if err != nil {
		return ClaimResult{}, err
	}
	return service.store.Claim(ctx, playerID, task, requestID, expectedSeasonKey, expectedDayKey, window, now)
}

var (
	seasonKeyPattern      = regexp.MustCompile(`^\d{4}-\d{2}$`)
	dayKeyPattern         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	publicPlayerIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
)

func WindowFor(now time.Time) (Window, error) {
	tokyo := TokyoTime(now)
	config, err := ConfigForMonth(int(tokyo.Month()))
	if err != nil {
		return Window{}, err
	}
	startTokyo := time.Date(tokyo.Year(), tokyo.Month(), 1, 0, 0, 0, 0, tokyo.Location())
	endTokyo := startTokyo.AddDate(0, 1, 0)
	nextResetTokyo := time.Date(tokyo.Year(), tokyo.Month(), tokyo.Day()+1, 0, 0, 0, 0, tokyo.Location())
	day := tokyo.Day()
	return Window{
		SeasonKey: tokyo.Format("2006-01"), DayKey: tokyo.Format("2006-01-02"), Day: day,
		Config: config, StartsAt: startTokyo.UTC(), EndsAt: endTokyo.UTC(), NextResetAt: nextResetTokyo.UTC(),
		RewardPeriodOpen: day >= 1 && day <= RewardDayCount,
	}, nil
}

func TokyoTime(now time.Time) time.Time {
	return now.In(time.FixedZone("Asia/Tokyo", 9*60*60))
}

// Package dailychallenge 负责每日挑战权威进度、固定奖励轮次与幂等结算。
package dailychallenge

import (
	"context"
	"errors"
	"regexp"
	"time"

	"linkgame-server/internal/player"
)

const (
	ChallengeLevelCount = 34
	UnlockLevel         = 10
)

var (
	ErrInvalidRequest         = errors.New("每日挑战请求不合法")
	ErrLocked                 = errors.New("每日挑战尚未解锁")
	ErrReplayRequired         = errors.New("每日挑战需要广告再次挑战资格")
	ErrReplayAlreadyAvailable = errors.New("每日挑战再次挑战资格已经存在")
	ErrAttemptNotFound        = errors.New("每日挑战轮次不存在")
	ErrAttemptCompleted       = errors.New("每日挑战轮次已经完成")
	ErrExpired                = errors.New("每日挑战轮次或资格已经过期")
	ErrStateConflict          = errors.New("每日挑战状态冲突")
	requestIDPattern          = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
)

type Reward struct {
	Type          string `json:"type"`
	ThemeID       *int   `json:"themeId,omitempty"`
	FragmentIndex *int   `json:"fragmentIndex,omitempty"`
	Quantity      int64  `json:"quantity"`
}

type Attempt struct {
	AttemptID      string     `json:"attemptId"`
	DayKey         string     `json:"dayKey"`
	ChallengeLevel int        `json:"challengeLevel"`
	ChallengeIndex int        `json:"challengeIndex"`
	Mode           string     `json:"mode"`
	Rewards        []Reward   `json:"rewards"`
	Status         string     `json:"status"`
	ClearSeconds   *int       `json:"clearSeconds"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt"`
}

type State struct {
	DayKey          string    `json:"dayKey"`
	Unlocked        bool      `json:"unlocked"`
	ChallengeLevel  int       `json:"challengeLevel"`
	ChallengeIndex  int       `json:"challengeIndex"`
	CompletionCount int       `json:"completionCount"`
	CompletedToday  bool      `json:"completedToday"`
	ReplayAvailable bool      `json:"replayAvailable"`
	ActiveAttempt   *Attempt  `json:"activeAttempt"`
	ServerTime      time.Time `json:"serverTime"`
}

type CompleteResult struct {
	AttemptID      string      `json:"attemptId"`
	GrantedRewards []Reward    `json:"grantedRewards"`
	Challenge      State       `json:"challenge"`
	Save           player.Save `json:"save"`
}

type Store interface {
	Get(context.Context, uint64, string, time.Time) (State, error)
	Start(context.Context, uint64, string, string, time.Time) (Attempt, error)
	Complete(context.Context, uint64, string, string, int, string, time.Time) (CompleteResult, error)
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
	return service.store.Get(ctx, playerID, TokyoDayKey(now), now)
}

func (service *Service) Start(ctx context.Context, playerID uint64, requestID string) (Attempt, error) {
	if service == nil || service.store == nil || playerID == 0 || !requestIDPattern.MatchString(requestID) {
		return Attempt{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	return service.store.Start(ctx, playerID, requestID, TokyoDayKey(now), now)
}

func (service *Service) Complete(ctx context.Context, playerID uint64, attemptID, requestID string, clearSeconds int) (CompleteResult, error) {
	if service == nil || service.store == nil || playerID == 0 ||
		!requestIDPattern.MatchString(attemptID) || !requestIDPattern.MatchString(requestID) ||
		clearSeconds < 0 || clearSeconds > 24*60*60 {
		return CompleteResult{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	return service.store.Complete(ctx, playerID, attemptID, requestID, clearSeconds, TokyoDayKey(now), now)
}

// TokyoDayKey 使用产品统一的东京自然日；日本当前没有夏令时，固定 +09:00 可避免系统缺失时区库。
func TokyoDayKey(now time.Time) string {
	return TokyoTime(now).Format("2006-01-02")
}

func TokyoTime(now time.Time) time.Time {
	return now.In(time.FixedZone("Asia/Tokyo", 9*60*60))
}

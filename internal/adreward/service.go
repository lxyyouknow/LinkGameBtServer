// Package adreward 负责服务端签发、核销激励广告的一次性奖励会话。
package adreward

import (
	"context"
	"errors"
	"regexp"
	"time"

	"linkgame-server/internal/dailychallenge"
	"linkgame-server/internal/player"
	"linkgame-server/internal/season"
)

type Placement string

const (
	PlacementHint                 Placement = "hint"
	PlacementShuffle              Placement = "shuffle"
	PlacementAutoRemove           Placement = "auto_remove"
	PlacementLevelComplete        Placement = "level_complete"
	PlacementDailyGift            Placement = "daily_gift"
	PlacementDailyChallengeReplay Placement = "daily_challenge_replay"
	PlacementSeasonMakeup         Placement = "season_makeup"
)

var (
	ErrInvalidRequest       = errors.New("广告奖励请求不合法")
	ErrNotFound             = errors.New("广告奖励会话不存在")
	ErrExpired              = errors.New("广告奖励会话已过期")
	ErrAlreadyClaimed       = errors.New("广告奖励已经领取")
	ErrIdempotencyKeyReused = errors.New("广告奖励幂等键已用于其他请求")
	ErrBusinessState        = errors.New("广告奖励业务状态不满足")
	identifierPattern       = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
)

type Session struct {
	SessionID   string    `json:"sessionId"`
	Placement   Placement `json:"placement"`
	BusinessKey string    `json:"businessKey"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type Reward struct {
	Type          string          `json:"type"`
	PropType      player.PropType `json:"propType,omitempty"`
	ThemeID       *int            `json:"themeId,omitempty"`
	FragmentIndex *int            `json:"fragmentIndex,omitempty"`
	Quantity      int64           `json:"quantity"`
}

type ClaimResult struct {
	SessionID      string                `json:"sessionId"`
	Placement      Placement             `json:"placement"`
	GrantedRewards []Reward              `json:"grantedRewards"`
	Save           player.Save           `json:"save"`
	Challenge      *dailychallenge.State `json:"challenge,omitempty"`
	Season         *season.State         `json:"season,omitempty"`
}

type Store interface {
	Create(context.Context, uint64, Placement, string, time.Time, time.Time) (Session, error)
	Claim(context.Context, uint64, string, string, string, time.Time) (ClaimResult, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

func (service *Service) Create(ctx context.Context, playerID uint64, placement Placement, businessKey string) (Session, error) {
	if service == nil || service.store == nil || playerID == 0 || !validPlacement(placement) || !identifierPattern.MatchString(businessKey) {
		return Session{}, ErrInvalidRequest
	}
	now := service.now().UTC()
	return service.store.Create(ctx, playerID, placement, businessKey, now, now.Add(10*time.Minute))
}

func (service *Service) Claim(ctx context.Context, playerID uint64, sessionID, requestID, adAttemptID string) (ClaimResult, error) {
	if service == nil || service.store == nil || playerID == 0 || !identifierPattern.MatchString(sessionID) ||
		!identifierPattern.MatchString(requestID) || (adAttemptID != "" && !identifierPattern.MatchString(adAttemptID)) {
		return ClaimResult{}, ErrInvalidRequest
	}
	return service.store.Claim(ctx, playerID, sessionID, requestID, adAttemptID, service.now().UTC())
}

func validPlacement(value Placement) bool {
	switch value {
	case PlacementHint, PlacementShuffle, PlacementAutoRemove, PlacementLevelComplete, PlacementDailyGift, PlacementDailyChallengeReplay, PlacementSeasonMakeup:
		return true
	default:
		return false
	}
}

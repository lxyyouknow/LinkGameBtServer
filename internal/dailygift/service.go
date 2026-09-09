// Package dailygift 负责每日广告礼包的服务端权威状态与领取规则。
package dailygift

import (
	"context"
	"errors"
	"regexp"
	"time"

	"linkgame-server/internal/player"
)

var (
	ErrInvalidRequest       = errors.New("每日礼包请求不合法")
	ErrNotFound             = errors.New("每日礼包不存在")
	ErrAlreadyClaimed       = errors.New("每日礼包已经领取")
	ErrExpired              = errors.New("每日礼包已经过期")
	ErrInsufficientRewards  = errors.New("可发放的限定主题碎片不足两个")
	ErrIdempotencyKeyReused = errors.New("每日礼包幂等键已用于其他请求")
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

const TokyoTimezone = "Asia/Tokyo"

// Reward 是由服务端决定并持久化的礼包奖励。
type Reward struct {
	Type          string          `json:"type"`
	PropType      player.PropType `json:"propType,omitempty"`
	ThemeID       *int            `json:"themeId,omitempty"`
	FragmentIndex *int            `json:"fragmentIndex,omitempty"`
	Quantity      int64           `json:"quantity"`
}

// Gift 是当前玩家某个东京自然日唯一的礼包。
type Gift struct {
	OfferID        string     `json:"offerId"`
	State          string     `json:"state"`
	BaseRewards    []Reward   `json:"baseRewards"`
	BonusRewards   []Reward   `json:"bonusRewards"`
	ClaimedAt      *time.Time `json:"claimedAt"`
	BonusClaimedAt *time.Time `json:"bonusClaimedAt"`
}

type QueryResult struct {
	ServerTime  time.Time `json:"serverTime"`
	DayKey      string    `json:"dayKey"`
	NextResetAt time.Time `json:"nextResetAt"`
	Gift        Gift      `json:"gift"`
}

type ClaimResult struct {
	Gift           Gift        `json:"gift"`
	GrantedRewards []Reward    `json:"grantedRewards"`
	Save           player.Save `json:"save"`
}

type Store interface {
	GetOrCreate(context.Context, uint64, string, time.Time) (Gift, error)
	Claim(context.Context, uint64, string, string, string, time.Time) (ClaimResult, error)
}

type Service struct {
	store Store
	now   func() time.Time
	zone  *time.Location
}

func NewService(store Store) *Service {
	return &Service{
		store: store,
		now:   time.Now,
		// 日本没有夏令时，使用固定 UTC+9 可避免精简 Linux 镜像缺少时区数据库。
		zone: time.FixedZone(TokyoTimezone, 9*60*60),
	}
}

func (service *Service) GetToday(ctx context.Context, playerID uint64) (QueryResult, error) {
	if service == nil || service.store == nil || playerID == 0 {
		return QueryResult{}, ErrInvalidRequest
	}
	now, dayKey, nextResetAt := service.window()
	gift, err := service.store.GetOrCreate(ctx, playerID, dayKey, now)
	if err != nil {
		return QueryResult{}, err
	}
	return QueryResult{
		ServerTime:  now,
		DayKey:      dayKey,
		NextResetAt: nextResetAt,
		Gift:        gift,
	}, nil
}

func (service *Service) Claim(
	ctx context.Context,
	playerID uint64,
	offerID string,
	requestID string,
) (ClaimResult, error) {
	if service == nil || service.store == nil || playerID == 0 ||
		!identifierPattern.MatchString(offerID) || !identifierPattern.MatchString(requestID) {
		return ClaimResult{}, ErrInvalidRequest
	}
	now, dayKey, _ := service.window()
	return service.store.Claim(ctx, playerID, dayKey, offerID, requestID, now)
}

func (service *Service) window() (time.Time, string, time.Time) {
	now := service.now().UTC()
	local := now.In(service.zone)
	nextLocal := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, service.zone)
	return now, local.Format("2006-01-02"), nextLocal.UTC()
}

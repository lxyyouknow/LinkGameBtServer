// Package leaderboard 负责服务端权威总排行榜与 TikTok 显式授权资料。
package leaderboard

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"linkgame-server/internal/platform"
)

const (
	MinLimit             = 1
	MaxLimit             = 30
	maxAuthorizationCode = 2048
	maxDisplayNameRunes  = 32
	maxAvatarURLBytes    = 2048
)

var (
	ErrInvalidLimit       = errors.New("排行榜 limit 不合法")
	ErrInvalidCode        = errors.New("排行榜资料授权 code 不合法")
	ErrIdentityMismatch   = errors.New("排行榜资料与当前玩家身份不匹配")
	ErrProfileUnavailable = errors.New("排行榜资料服务暂不可用")
)

type Entry struct {
	Rank         *int   `json:"rank"`
	DisplayName  string `json:"displayName"`
	PlayerNumber uint64 `json:"playerNumber"`
	AvatarURL    string `json:"avatarUrl,omitempty"`
	Level        int    `json:"level"`
	IsSelf       bool   `json:"isSelf"`
}

type Snapshot struct {
	Scope      string    `json:"scope"`
	Entries    []Entry   `json:"entries"`
	Self       Entry     `json:"self"`
	ServerTime time.Time `json:"serverTime"`
}

type Profile struct {
	Status      string    `json:"status"`
	DisplayName string    `json:"displayName"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Store interface {
	Global(context.Context, uint64, int) ([]Entry, Entry, error)
	UpdateTikTokProfile(context.Context, uint64, platform.AuthorizedProfile, time.Time) (Profile, error)
	InvalidateTikTokProfile(context.Context, uint64, time.Time) error
}

type ProfileAuthorizer interface {
	FetchAuthorizedProfile(context.Context, string) (platform.AuthorizedProfile, error)
}

type Service struct {
	store      Store
	authorizer ProfileAuthorizer
	now        func() time.Time
}

func NewService(store Store, authorizer ProfileAuthorizer) *Service {
	return &Service{store: store, authorizer: authorizer, now: time.Now}
}

func (service *Service) Global(ctx context.Context, playerID uint64, limit int) (Snapshot, error) {
	if playerID == 0 || limit < MinLimit || limit > MaxLimit {
		return Snapshot{}, ErrInvalidLimit
	}
	entries, self, err := service.store.Global(ctx, playerID, limit)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Scope: "global", Entries: entries, Self: self, ServerTime: service.now().UTC()}, nil
}

func (service *Service) AuthorizeTikTokProfile(ctx context.Context, playerID uint64, code string) (Profile, error) {
	if playerID == 0 || code == "" || len(code) > maxAuthorizationCode || strings.TrimSpace(code) != code {
		return Profile{}, ErrInvalidCode
	}
	if service.authorizer == nil {
		return Profile{}, ErrProfileUnavailable
	}
	profile, err := service.authorizer.FetchAuthorizedProfile(ctx, code)
	if err != nil {
		if errors.Is(err, platform.ErrProfileUnauthorized) {
			_ = service.store.InvalidateTikTokProfile(ctx, playerID, service.now().UTC())
		}
		return Profile{}, err
	}
	if profile.Provider != "tiktok" || profile.ProviderUID == "" {
		return Profile{}, ErrIdentityMismatch
	}
	profile.DisplayName = normalizeDisplayName(profile.DisplayName)
	profile.AvatarURL = normalizeAvatarURL(profile.AvatarURL)
	return service.store.UpdateTikTokProfile(ctx, playerID, profile, service.now().UTC())
}

func DefaultDisplayName(playerNumber uint64) string {
	digits := uintToDecimal(playerNumber)
	if len(digits) < 6 {
		digits = strings.Repeat("0", 6-len(digits)) + digits
	}
	return "USER" + digits
}

func normalizeDisplayName(value string) string {
	value = strings.TrimSpace(value)
	filtered := make([]rune, 0, min(utf8.RuneCountInString(value), maxDisplayNameRunes))
	for _, character := range value {
		if unicode.IsControl(character) {
			continue
		}
		filtered = append(filtered, character)
		if len(filtered) == maxDisplayNameRunes {
			break
		}
	}
	return strings.TrimSpace(string(filtered))
}

func normalizeAvatarURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxAvatarURLBytes {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func uintToDecimal(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}

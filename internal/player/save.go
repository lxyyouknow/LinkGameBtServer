// Package player 负责 LinkGame 云存档与低价值游戏资产的业务校验。
package player

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"time"
)

const (
	MinLevel                        = 0
	MaxLevel                        = 999
	ThemeCount                      = 59
	NormalThemeFragmentCount        = 27
	LimitedThemeFragmentCount       = 34
	MaxCoins                  int64 = 2_147_483_647
	MaxPropCount              int64 = 2_147_483_647
	MaxMutationBatch                = 100
)

// PropCounts 表示三类服务端权威道具库存。
type PropCounts struct {
	Hint    int64
	Shuffle int64
	Remove  int64
}

// InitialPropCounts 是所有登录渠道创建新玩家存档时唯一使用的初始库存工厂。
func InitialPropCounts() PropCounts {
	return PropCounts{}
}

var (
	ErrInvalidSave          = errors.New("存档参数不合法")
	ErrRevisionConflict     = errors.New("存档 revision 冲突")
	ErrInvalidCoinMutation  = errors.New("金币变更参数不合法")
	ErrInvalidPropMutation  = errors.New("道具变更参数不合法")
	ErrInvalidThemeMutation = errors.New("主题碎片变更参数不合法")
	ErrInsufficientCoins    = errors.New("金币余额不足")
	ErrCoinLimitExceeded    = errors.New("金币余额超过上限")
	ErrInsufficientProps    = errors.New("道具剩余次数不足")
	ErrPropLimitExceeded    = errors.New("道具剩余次数超过上限")
	ErrThemeLocked          = errors.New("主题尚未完整激活")
)

var (
	clientVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{0,64}$`)
	mutationIDPattern    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	missionIDPattern     = regexp.MustCompile(`^tiktok:(home_shortcut|profile_revisit):`)
	limitedThemeIDs      = []int{7, 13, 17, 20, 25, 28, 31, 32, 36, 37, 43, 47, 50, 53, 56, 57, 58}
)

// TikTokMissionKey 是需要跨设备保存领取状态的平台任务。
type TikTokMissionKey string

const (
	TikTokMissionHomeShortcut   TikTokMissionKey = "home_shortcut:v1"
	TikTokMissionProfileRevisit TikTokMissionKey = "profile_revisit:v1"

	HomeShortcutHintMutationID  = "tiktok:home_shortcut:v1:hint-v2"
	HomeShortcutThemeMutationID = "tiktok:home_shortcut:v1:theme-v2"
	HomeShortcutLegacyCoinID    = "tiktok:home_shortcut:v1:coin"
	HomeShortcutLegacyHintID    = "tiktok:home_shortcut:v1:hint"
	HomeShortcutLegacyShuffleID = "tiktok:home_shortcut:v1:shuffle"
	ProfileRemoveMutationID     = "tiktok:profile_revisit:v1:remove"
	ProfileThemeMutationID      = "tiktok:profile_revisit:v1:theme"
	ProfileLegacyCoinID         = "tiktok:profile_revisit:v1:coin"
	ProfileLegacyHintID         = "tiktok:profile_revisit:v1:hint"
	ProfileLegacyShuffleID      = "tiktok:profile_revisit:v1:shuffle"
)

type CoinMutationReason string

const (
	CoinReasonLevelComplete CoinMutationReason = "level_complete"
	CoinReasonGiftReward    CoinMutationReason = "gift_reward"
)

type CoinMutation struct {
	ID        string             `json:"id"`
	Delta     int64              `json:"delta"`
	Reason    CoinMutationReason `json:"reason"`
	CreatedAt string             `json:"createdAt"`
}

type PropType string

const (
	PropTypeHint    PropType = "hint"
	PropTypeShuffle PropType = "shuffle"
	PropTypeRemove  PropType = "remove"
)

type PropMutationReason string

const (
	PropReasonUse           PropMutationReason = "use"
	PropReasonLevelComplete PropMutationReason = "level_complete"
	PropReasonGiftReward    PropMutationReason = "gift_reward"
)

type PropMutation struct {
	ID        string             `json:"id"`
	PropType  PropType           `json:"propType"`
	Delta     int64              `json:"delta"`
	Reason    PropMutationReason `json:"reason"`
	CreatedAt string             `json:"createdAt"`
}

type ThemeMutationReason string

const (
	ThemeReasonLevelComplete ThemeMutationReason = "level_complete"
	ThemeReasonEventReward   ThemeMutationReason = "event_reward"
)

// ThemeFragmentMutation 表示获得一块主题碎片。没有负向操作，已经获得的碎片不会被删除。
type ThemeFragmentMutation struct {
	ID            string              `json:"id"`
	ThemeID       int                 `json:"themeId"`
	FragmentIndex int                 `json:"fragmentIndex"`
	Reason        ThemeMutationReason `json:"reason"`
	CreatedAt     string              `json:"createdAt"`
}

// Save 是客户端读取到的完整云存档快照。
type Save struct {
	Revision                         uint64
	Level                            int
	SelectedTheme                    int
	CollectingTheme                  int
	Coins                            int64
	HintCount                        int64
	ShuffleCount                     int64
	RemoveCount                      int64
	ThemeFragments                   [][]int
	SoundEnabled                     bool
	MusicEnabled                     bool
	EffectsEnabled                   bool
	VibrationEnabled                 bool
	TutorialCompleted                bool
	AcceptedCoinMutationIDs          []string
	AcceptedPropMutationIDs          []string
	AcceptedThemeFragmentMutationIDs []string
	ClaimedTikTokMissions            []TikTokMissionKey
	ClientVersion                    string
	UpdatedAt                        time.Time
}

type UpdateSaveInput struct {
	Revision               uint64
	Level                  int
	SelectedTheme          int
	CollectingTheme        int
	SoundEnabled           bool
	MusicEnabled           bool
	EffectsEnabled         bool
	VibrationEnabled       bool
	TutorialCompleted      bool
	CoinMutations          []CoinMutation
	PropMutations          []PropMutation
	ThemeFragmentMutations []ThemeFragmentMutation
	ClientVersion          string
}

type Store interface {
	GetSave(ctx context.Context, playerID uint64) (Save, error)
	UpdateSave(ctx context.Context, playerID uint64, input UpdateSaveInput) (Save, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (service *Service) GetSave(ctx context.Context, playerID uint64) (Save, error) {
	return service.store.GetSave(ctx, playerID)
}

func (service *Service) UpdateSave(ctx context.Context, playerID uint64, input UpdateSaveInput) (Save, error) {
	if input.Revision < 1 || input.Level < MinLevel || input.Level > MaxLevel ||
		!ValidThemeID(input.SelectedTheme) || !ValidThemeID(input.CollectingTheme) ||
		IsLimitedTheme(input.CollectingTheme) || !clientVersionPattern.MatchString(input.ClientVersion) {
		return Save{}, ErrInvalidSave
	}
	if err := validateCoinMutations(input.CoinMutations); err != nil {
		return Save{}, err
	}
	if err := validatePropMutations(input.PropMutations); err != nil {
		return Save{}, err
	}
	if err := validateThemeMutations(input.ThemeFragmentMutations); err != nil {
		return Save{}, err
	}
	return service.store.UpdateSave(ctx, playerID, input)
}

func ValidThemeID(themeID int) bool { return themeID >= 0 && themeID < ThemeCount }

func IsLimitedTheme(themeID int) bool { return slices.Contains(limitedThemeIDs, themeID) }

func ThemeFragmentCount(themeID int) int {
	if IsLimitedTheme(themeID) {
		return LimitedThemeFragmentCount
	}
	return NormalThemeFragmentCount
}

func DefaultThemeFragments() [][]int {
	fragments := make([][]int, ThemeCount)
	fragments[0] = make([]int, NormalThemeFragmentCount)
	for index := range fragments[0] {
		fragments[0][index] = index
	}
	return fragments
}

func validateCoinMutations(mutations []CoinMutation) error {
	if len(mutations) > MaxMutationBatch {
		return ErrInvalidCoinMutation
	}
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !mutationIDPattern.MatchString(mutation.ID) || mutation.Delta == 0 ||
			mutation.Delta > MaxCoins || mutation.Delta < -MaxCoins || !validTime(mutation.CreatedAt) {
			return ErrInvalidCoinMutation
		}
		if _, exists := seen[mutation.ID]; exists {
			return ErrInvalidCoinMutation
		}
		seen[mutation.ID] = struct{}{}
		if isReservedTikTokMissionMutationID(mutation.ID) {
			if (mutation.ID != HomeShortcutLegacyCoinID && mutation.ID != ProfileLegacyCoinID) ||
				mutation.Delta != 300 || mutation.Reason != CoinReasonGiftReward {
				return ErrInvalidCoinMutation
			}
			continue
		}
		switch mutation.Reason {
		case CoinReasonLevelComplete:
			if mutation.Delta < 1 || mutation.Delta > 1_000 {
				return ErrInvalidCoinMutation
			}
		case CoinReasonGiftReward:
			if mutation.Delta < 1 || mutation.Delta > 500 {
				return ErrInvalidCoinMutation
			}
		default:
			return ErrInvalidCoinMutation
		}
	}
	return nil
}

func validatePropMutations(mutations []PropMutation) error {
	if len(mutations) > MaxMutationBatch {
		return ErrInvalidPropMutation
	}
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !mutationIDPattern.MatchString(mutation.ID) || !validTime(mutation.CreatedAt) ||
			(mutation.PropType != PropTypeHint && mutation.PropType != PropTypeShuffle && mutation.PropType != PropTypeRemove) {
			return ErrInvalidPropMutation
		}
		if _, exists := seen[mutation.ID]; exists {
			return ErrInvalidPropMutation
		}
		seen[mutation.ID] = struct{}{}
		if isReservedTikTokMissionMutationID(mutation.ID) {
			switch mutation.ID {
			case HomeShortcutHintMutationID:
				if mutation.PropType != PropTypeHint || mutation.Delta != 3 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			case ProfileRemoveMutationID:
				if mutation.PropType != PropTypeRemove || mutation.Delta != 3 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			case HomeShortcutLegacyHintID:
				if mutation.PropType != PropTypeHint || mutation.Delta != 1 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			case HomeShortcutLegacyShuffleID:
				if mutation.PropType != PropTypeShuffle || mutation.Delta != 1 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			case ProfileLegacyHintID:
				if mutation.PropType != PropTypeHint || mutation.Delta != 1 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			case ProfileLegacyShuffleID:
				if mutation.PropType != PropTypeShuffle || mutation.Delta != 1 || mutation.Reason != PropReasonGiftReward {
					return ErrInvalidPropMutation
				}
			default:
				return ErrInvalidPropMutation
			}
			continue
		}
		switch mutation.Reason {
		case PropReasonUse:
			if mutation.Delta != -1 {
				return ErrInvalidPropMutation
			}
		case PropReasonLevelComplete, PropReasonGiftReward:
			if mutation.Delta < 1 || mutation.Delta > 10 {
				return ErrInvalidPropMutation
			}
		default:
			return ErrInvalidPropMutation
		}
	}
	return nil
}

func isReservedTikTokMissionMutationID(identifier string) bool {
	return missionIDPattern.MatchString(identifier)
}

func validateThemeMutations(mutations []ThemeFragmentMutation) error {
	if len(mutations) > MaxMutationBatch {
		return ErrInvalidThemeMutation
	}
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !mutationIDPattern.MatchString(mutation.ID) || !validTime(mutation.CreatedAt) ||
			!ValidThemeID(mutation.ThemeID) || mutation.ThemeID == 0 ||
			mutation.FragmentIndex < 0 || mutation.FragmentIndex >= ThemeFragmentCount(mutation.ThemeID) {
			return ErrInvalidThemeMutation
		}
		if _, exists := seen[mutation.ID]; exists {
			return ErrInvalidThemeMutation
		}
		seen[mutation.ID] = struct{}{}
		if isReservedTikTokMissionMutationID(mutation.ID) {
			switch mutation.ID {
			case HomeShortcutThemeMutationID, ProfileThemeMutationID:
				if mutation.Reason != ThemeReasonLevelComplete || !IsLimitedTheme(mutation.ThemeID) {
					return ErrInvalidThemeMutation
				}
			default:
				return ErrInvalidThemeMutation
			}
			continue
		}
		switch mutation.Reason {
		case ThemeReasonLevelComplete:
			if IsLimitedTheme(mutation.ThemeID) {
				return ErrInvalidThemeMutation
			}
		case ThemeReasonEventReward:
			// 活动系统接入后仍复用该流水；当前客户端不应自行生成该原因。
		default:
			return ErrInvalidThemeMutation
		}
	}
	return nil
}

func validTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

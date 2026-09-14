package player

import (
	"context"
	"errors"
	"testing"
)

type fakeStore struct{ updated UpdateSaveInput }

func (store *fakeStore) GetSave(context.Context, uint64) (Save, error) { return Save{}, nil }
func (store *fakeStore) UpdateSave(_ context.Context, _ uint64, input UpdateSaveInput) (Save, error) {
	store.updated = input
	return Save{Revision: input.Revision + 1}, nil
}

func validInput() UpdateSaveInput {
	return UpdateSaveInput{Revision: 1, Level: 2, SelectedTheme: 0, CollectingTheme: 1, ClientVersion: "1.0.0"}
}

func TestUpdateSave接受LinkGame字段(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	input := validInput()
	input.PropMutations = []PropMutation{{ID: "use-1", PropType: PropTypeRemove, Delta: -1, Reason: PropReasonUse, CreatedAt: "2026-08-20T00:00:00Z"}}
	input.ThemeFragmentMutations = []ThemeFragmentMutation{{ID: "theme-1", ThemeID: 1, FragmentIndex: 3, Reason: ThemeReasonLevelComplete, CreatedAt: "2026-08-20T00:00:00Z"}}
	if _, err := service.UpdateSave(context.Background(), 7, input); err != nil {
		t.Fatalf("UpdateSave() error = %v", err)
	}
}

func TestUpdateSave拒绝限定主题普通通关碎片(t *testing.T) {
	service := NewService(&fakeStore{})
	input := validInput()
	input.ThemeFragmentMutations = []ThemeFragmentMutation{{ID: "theme-7", ThemeID: 7, FragmentIndex: 0, Reason: ThemeReasonLevelComplete, CreatedAt: "2026-08-20T00:00:00Z"}}
	_, err := service.UpdateSave(context.Background(), 7, input)
	if !errors.Is(err, ErrInvalidThemeMutation) {
		t.Fatalf("error = %v, want ErrInvalidThemeMutation", err)
	}
}

func TestUpdateSave接受添加桌面与侧边栏限定主题奖励(t *testing.T) {
	service := NewService(&fakeStore{})
	createdAt := "2026-08-29T00:00:00Z"
	input := validInput()
	input.PropMutations = []PropMutation{
		{ID: HomeShortcutHintMutationID, PropType: PropTypeHint, Delta: 3, Reason: PropReasonGiftReward, CreatedAt: createdAt},
		{ID: ProfileRemoveMutationID, PropType: PropTypeRemove, Delta: 3, Reason: PropReasonGiftReward, CreatedAt: createdAt},
	}
	input.ThemeFragmentMutations = []ThemeFragmentMutation{
		{ID: HomeShortcutThemeMutationID, ThemeID: 7, FragmentIndex: 0, Reason: ThemeReasonLevelComplete, CreatedAt: createdAt},
		{ID: ProfileThemeMutationID, ThemeID: 13, FragmentIndex: 1, Reason: ThemeReasonLevelComplete, CreatedAt: createdAt},
	}
	if _, err := service.UpdateSave(context.Background(), 7, input); err != nil {
		t.Fatalf("UpdateSave() error = %v", err)
	}
}

func TestUpdateSave继续接受旧版添加桌面奖励流水(t *testing.T) {
	service := NewService(&fakeStore{})
	createdAt := "2026-08-29T00:00:00Z"
	input := validInput()
	input.CoinMutations = []CoinMutation{{
		ID: HomeShortcutLegacyCoinID, Delta: 300, Reason: CoinReasonGiftReward, CreatedAt: createdAt,
	}}
	input.PropMutations = []PropMutation{
		{ID: HomeShortcutLegacyHintID, PropType: PropTypeHint, Delta: 1, Reason: PropReasonGiftReward, CreatedAt: createdAt},
		{ID: HomeShortcutLegacyShuffleID, PropType: PropTypeShuffle, Delta: 1, Reason: PropReasonGiftReward, CreatedAt: createdAt},
	}
	if _, err := service.UpdateSave(context.Background(), 7, input); err != nil {
		t.Fatalf("UpdateSave() error = %v", err)
	}
}

func TestUpdateSave拒绝伪造平台任务奖励参数(t *testing.T) {
	service := NewService(&fakeStore{})
	input := validInput()
	input.PropMutations = []PropMutation{{
		ID: HomeShortcutHintMutationID, PropType: PropTypeHint, Delta: 10,
		Reason: PropReasonGiftReward, CreatedAt: "2026-08-29T00:00:00Z",
	}}
	_, err := service.UpdateSave(context.Background(), 7, input)
	if !errors.Is(err, ErrInvalidPropMutation) {
		t.Fatalf("error = %v, want ErrInvalidPropMutation", err)
	}
}

func TestUpdateSave拒绝未知平台任务流水(t *testing.T) {
	service := NewService(&fakeStore{})
	input := validInput()
	input.PropMutations = []PropMutation{{
		ID: "tiktok:home_shortcut:v1:unknown", PropType: PropTypeHint, Delta: 3,
		Reason: PropReasonGiftReward, CreatedAt: "2026-08-29T00:00:00Z",
	}}
	_, err := service.UpdateSave(context.Background(), 7, input)
	if !errors.Is(err, ErrInvalidPropMutation) {
		t.Fatalf("error = %v, want ErrInvalidPropMutation", err)
	}
}

func TestUpdateSave拒绝非法道具方向(t *testing.T) {
	service := NewService(&fakeStore{})
	input := validInput()
	input.PropMutations = []PropMutation{{ID: "use-1", PropType: PropTypeHint, Delta: 1, Reason: PropReasonUse, CreatedAt: "2026-08-20T00:00:00Z"}}
	_, err := service.UpdateSave(context.Background(), 7, input)
	if !errors.Is(err, ErrInvalidPropMutation) {
		t.Fatalf("error = %v, want ErrInvalidPropMutation", err)
	}
}

func TestThemeFragmentCount(t *testing.T) {
	if got := ThemeFragmentCount(1); got != 27 {
		t.Fatalf("normal = %d", got)
	}
	if got := ThemeFragmentCount(58); got != 34 {
		t.Fatalf("limited = %d", got)
	}
}

func TestInitialPropCounts全部为一(t *testing.T) {
	counts := InitialPropCounts()
	if counts.Hint != 1 || counts.Shuffle != 1 || counts.Remove != 1 {
		t.Fatalf("初始道具库存 = %#v，期望全部为 1", counts)
	}
}

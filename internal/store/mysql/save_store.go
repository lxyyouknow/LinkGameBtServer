package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"linkgame-server/internal/player"
)

type SaveStore struct{ db *sql.DB }

func NewSaveStore(db *sql.DB) *SaveStore { return &SaveStore{db: db} }

type tiktokMissionSubmission struct {
	key            player.TikTokMissionKey
	coinMutations  []*player.CoinMutation
	propMutations  []*player.PropMutation
	themeMutation  *player.ThemeFragmentMutation
	legacyBundle   bool
	alreadyClaimed bool
}

func (store *SaveStore) GetSave(ctx context.Context, playerID uint64) (player.Save, error) {
	if err := ensureSave(ctx, store.db, playerID); err != nil {
		return player.Save{}, err
	}
	return selectSave(ctx, store.db, playerID)
}

func (store *SaveStore) UpdateSave(ctx context.Context, playerID uint64, input player.UpdateSaveInput) (player.Save, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return player.Save{}, fmt.Errorf("开始更新玩家存档事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSave(ctx, tx, playerID); err != nil {
		return player.Save{}, err
	}

	var revision uint64
	var coins, hints, shuffles, removes int64
	if err := tx.QueryRowContext(ctx, `SELECT revision, coins, hint_count, shuffle_count, remove_count FROM player_saves WHERE player_id = ? FOR UPDATE`, playerID).
		Scan(&revision, &coins, &hints, &shuffles, &removes); err != nil {
		return player.Save{}, fmt.Errorf("锁定玩家存档失败: %w", err)
	}
	if revision != input.Revision {
		return player.Save{}, player.ErrRevisionConflict
	}

	missionSubmissions, err := prepareTikTokMissionSubmissions(ctx, tx, playerID, input)
	if err != nil {
		return player.Save{}, err
	}
	skippedCoinMutationIDs := make(map[string]struct{})
	skippedPropMutationIDs := make(map[string]struct{})
	skippedThemeMutationIDs := make(map[string]struct{})
	for _, submission := range missionSubmissions {
		if !submission.alreadyClaimed {
			continue
		}
		for _, mutation := range submission.coinMutations {
			skippedCoinMutationIDs[mutation.ID] = struct{}{}
		}
		for _, mutation := range submission.propMutations {
			skippedPropMutationIDs[mutation.ID] = struct{}{}
		}
		if submission.themeMutation != nil {
			skippedThemeMutationIDs[submission.themeMutation.ID] = struct{}{}
		}
	}

	acceptedCoins := make([]string, 0, len(input.CoinMutations))
	for _, mutation := range input.CoinMutations {
		if _, skipped := skippedCoinMutationIDs[mutation.ID]; skipped {
			acceptedCoins = append(acceptedCoins, mutation.ID)
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_coin_mutations (player_id, mutation_id, delta, reason, client_version, created_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, playerID, mutation.ID, mutation.Delta, mutation.Reason, input.ClientVersion)
		if err != nil {
			return player.Save{}, fmt.Errorf("记录金币流水失败: %w", err)
		}
		inserted, err := affected(result)
		if err != nil {
			return player.Save{}, err
		}
		if inserted {
			if mutation.Delta < 0 && coins < -mutation.Delta {
				return player.Save{}, player.ErrInsufficientCoins
			}
			if mutation.Delta > 0 && coins > player.MaxCoins-mutation.Delta {
				return player.Save{}, player.ErrCoinLimitExceeded
			}
			coins += mutation.Delta
		} else {
			var delta int64
			var reason player.CoinMutationReason
			if err := tx.QueryRowContext(ctx, `SELECT delta, reason FROM player_coin_mutations WHERE player_id = ? AND mutation_id = ?`, playerID, mutation.ID).Scan(&delta, &reason); err != nil {
				return player.Save{}, fmt.Errorf("核对重复金币流水失败: %w", err)
			}
			if delta != mutation.Delta || reason != mutation.Reason {
				return player.Save{}, player.ErrInvalidCoinMutation
			}
		}
		acceptedCoins = append(acceptedCoins, mutation.ID)
	}

	acceptedProps := make([]string, 0, len(input.PropMutations))
	for _, mutation := range input.PropMutations {
		if _, skipped := skippedPropMutationIDs[mutation.ID]; skipped {
			acceptedProps = append(acceptedProps, mutation.ID)
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_prop_mutations (player_id, mutation_id, prop_type, delta, reason, client_version, created_at) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, playerID, mutation.ID, mutation.PropType, mutation.Delta, mutation.Reason, input.ClientVersion)
		if err != nil {
			return player.Save{}, fmt.Errorf("记录道具流水失败: %w", err)
		}
		inserted, err := affected(result)
		if err != nil {
			return player.Save{}, err
		}
		if inserted {
			count := propCountPointer(mutation.PropType, &hints, &shuffles, &removes)
			if mutation.Delta < 0 && *count < -mutation.Delta {
				return player.Save{}, player.ErrInsufficientProps
			}
			if mutation.Delta > 0 && *count > player.MaxPropCount-mutation.Delta {
				return player.Save{}, player.ErrPropLimitExceeded
			}
			*count += mutation.Delta
		} else {
			var propType player.PropType
			var delta int64
			var reason player.PropMutationReason
			if err := tx.QueryRowContext(ctx, `SELECT prop_type, delta, reason FROM player_prop_mutations WHERE player_id = ? AND mutation_id = ?`, playerID, mutation.ID).Scan(&propType, &delta, &reason); err != nil {
				return player.Save{}, fmt.Errorf("核对重复道具流水失败: %w", err)
			}
			if propType != mutation.PropType || delta != mutation.Delta || reason != mutation.Reason {
				return player.Save{}, player.ErrInvalidPropMutation
			}
		}
		acceptedProps = append(acceptedProps, mutation.ID)
	}

	acceptedThemes := make([]string, 0, len(input.ThemeFragmentMutations))
	for _, mutation := range input.ThemeFragmentMutations {
		if _, skipped := skippedThemeMutationIDs[mutation.ID]; skipped {
			acceptedThemes = append(acceptedThemes, mutation.ID)
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_mutations (player_id, mutation_id, theme_id, fragment_index, reason, client_version, created_at) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, playerID, mutation.ID, mutation.ThemeID, mutation.FragmentIndex, mutation.Reason, input.ClientVersion)
		if err != nil {
			return player.Save{}, fmt.Errorf("记录主题碎片流水失败: %w", err)
		}
		inserted, err := affected(result)
		if err != nil {
			return player.Save{}, err
		}
		if inserted {
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO player_theme_fragments (player_id, theme_id, fragment_index, acquired_at) VALUES (?, ?, ?, UTC_TIMESTAMP(6))`, playerID, mutation.ThemeID, mutation.FragmentIndex); err != nil {
				return player.Save{}, fmt.Errorf("写入主题碎片失败: %w", err)
			}
		} else {
			var themeID, fragmentIndex int
			var reason player.ThemeMutationReason
			if err := tx.QueryRowContext(ctx, `SELECT theme_id, fragment_index, reason FROM player_theme_mutations WHERE player_id = ? AND mutation_id = ?`, playerID, mutation.ID).Scan(&themeID, &fragmentIndex, &reason); err != nil {
				return player.Save{}, fmt.Errorf("核对重复主题碎片流水失败: %w", err)
			}
			if themeID != mutation.ThemeID || fragmentIndex != mutation.FragmentIndex || reason != mutation.Reason {
				return player.Save{}, player.ErrInvalidThemeMutation
			}
		}
		acceptedThemes = append(acceptedThemes, mutation.ID)
	}

	for _, submission := range missionSubmissions {
		if submission.alreadyClaimed {
			continue
		}
		var themeMutationID any
		if submission.themeMutation != nil {
			themeMutationID = submission.themeMutation.ID
		}
		var propMutationID any
		if len(submission.propMutations) > 0 {
			propMutationID = submission.propMutations[0].ID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_tiktok_mission_claims
			(player_id,mission_key,prop_mutation_id,theme_mutation_id,claimed_at)
			VALUES (?,?,?,?,UTC_TIMESTAMP(6))`, playerID, submission.key, propMutationID, themeMutationID); err != nil {
			return player.Save{}, fmt.Errorf("记录 TikTok 任务领取状态失败: %w", err)
		}
	}

	if input.SelectedTheme != 0 {
		var fragmentCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_theme_fragments WHERE player_id = ? AND theme_id = ?`, playerID, input.SelectedTheme).Scan(&fragmentCount); err != nil {
			return player.Save{}, fmt.Errorf("检查主题激活状态失败: %w", err)
		}
		if fragmentCount < player.ThemeFragmentCount(input.SelectedTheme) {
			return player.Save{}, player.ErrThemeLocked
		}
	}

	result, err := tx.ExecContext(ctx, `UPDATE player_saves SET revision = revision + 1, reached_level_at = CASE WHEN ? > level THEN UTC_TIMESTAMP(6) WHEN reached_level_at IS NULL THEN updated_at ELSE reached_level_at END, level = GREATEST(level, ?), selected_theme = ?, collecting_theme = ?, coins = ?, hint_count = ?, shuffle_count = ?, remove_count = ?, sound_enabled = ?, music_enabled = ?, effects_enabled = ?, vibration_enabled = ?, tutorial_completed = (tutorial_completed OR ?), client_version = ?, updated_at = UTC_TIMESTAMP(6) WHERE player_id = ? AND revision = ?`,
		input.Level, input.Level, input.SelectedTheme, input.CollectingTheme, coins, hints, shuffles, removes,
		input.SoundEnabled, input.MusicEnabled, input.EffectsEnabled, input.VibrationEnabled, input.TutorialCompleted,
		input.ClientVersion, playerID, input.Revision)
	if err != nil {
		return player.Save{}, fmt.Errorf("更新玩家存档失败: %w", err)
	}
	updated, err := affected(result)
	if err != nil {
		return player.Save{}, err
	}
	if !updated {
		return player.Save{}, player.ErrRevisionConflict
	}

	save, err := selectSave(ctx, tx, playerID)
	if err != nil {
		return player.Save{}, err
	}
	save.AcceptedCoinMutationIDs = acceptedCoins
	save.AcceptedPropMutationIDs = acceptedProps
	save.AcceptedThemeFragmentMutationIDs = acceptedThemes
	if err := tx.Commit(); err != nil {
		return player.Save{}, fmt.Errorf("提交玩家存档事务失败: %w", err)
	}
	return save, nil
}

func propCountPointer(propType player.PropType, hints, shuffles, removes *int64) *int64 {
	switch propType {
	case player.PropTypeShuffle:
		return shuffles
	case player.PropTypeRemove:
		return removes
	default:
		return hints
	}
}

func affected(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取数据库写入结果失败: %w", err)
	}
	return rows == 1, nil
}

func prepareTikTokMissionSubmissions(
	ctx context.Context,
	tx *sql.Tx,
	playerID uint64,
	input player.UpdateSaveInput,
) ([]*tiktokMissionSubmission, error) {
	byKey := map[player.TikTokMissionKey]*tiktokMissionSubmission{}
	ensure := func(key player.TikTokMissionKey) *tiktokMissionSubmission {
		if existing := byKey[key]; existing != nil {
			return existing
		}
		created := &tiktokMissionSubmission{key: key}
		byKey[key] = created
		return created
	}
	for index := range input.CoinMutations {
		mutation := &input.CoinMutations[index]
		switch mutation.ID {
		case player.HomeShortcutLegacyCoinID:
			submission := ensure(player.TikTokMissionHomeShortcut)
			submission.legacyBundle = true
			submission.coinMutations = append(submission.coinMutations, mutation)
		case player.ProfileLegacyCoinID:
			submission := ensure(player.TikTokMissionProfileRevisit)
			submission.legacyBundle = true
			submission.coinMutations = append(submission.coinMutations, mutation)
		}
	}
	for index := range input.PropMutations {
		mutation := &input.PropMutations[index]
		switch mutation.ID {
		case player.HomeShortcutHintMutationID:
			submission := ensure(player.TikTokMissionHomeShortcut)
			submission.propMutations = append(submission.propMutations, mutation)
		case player.HomeShortcutLegacyHintID, player.HomeShortcutLegacyShuffleID:
			submission := ensure(player.TikTokMissionHomeShortcut)
			submission.legacyBundle = true
			submission.propMutations = append(submission.propMutations, mutation)
		case player.ProfileLegacyHintID, player.ProfileLegacyShuffleID:
			submission := ensure(player.TikTokMissionProfileRevisit)
			submission.legacyBundle = true
			submission.propMutations = append(submission.propMutations, mutation)
		case player.ProfileRemoveMutationID:
			submission := ensure(player.TikTokMissionProfileRevisit)
			submission.propMutations = append(submission.propMutations, mutation)
		}
	}
	for index := range input.ThemeFragmentMutations {
		mutation := &input.ThemeFragmentMutations[index]
		switch mutation.ID {
		case player.HomeShortcutThemeMutationID:
			ensure(player.TikTokMissionHomeShortcut).themeMutation = mutation
		case player.ProfileThemeMutationID:
			ensure(player.TikTokMissionProfileRevisit).themeMutation = mutation
		}
	}

	orderedKeys := []player.TikTokMissionKey{
		player.TikTokMissionHomeShortcut,
		player.TikTokMissionProfileRevisit,
	}
	result := make([]*tiktokMissionSubmission, 0, len(byKey))
	for _, key := range orderedKeys {
		submission := byKey[key]
		if submission == nil {
			continue
		}
		if len(submission.propMutations) == 0 {
			return nil, player.ErrInvalidPropMutation
		}
		if submission.legacyBundle {
			legacyHintID, legacyShuffleID := player.HomeShortcutLegacyHintID, player.HomeShortcutLegacyShuffleID
			if key == player.TikTokMissionProfileRevisit {
				legacyHintID, legacyShuffleID = player.ProfileLegacyHintID, player.ProfileLegacyShuffleID
			}
			if len(submission.coinMutations) != 1 || len(submission.propMutations) != 2 ||
				submission.themeMutation != nil || !hasPropMutation(submission.propMutations, legacyHintID) ||
				!hasPropMutation(submission.propMutations, legacyShuffleID) {
				return nil, player.ErrInvalidSave
			}
		} else if len(submission.coinMutations) != 0 || len(submission.propMutations) != 1 {
			return nil, player.ErrInvalidSave
		}
		var claimedKey string
		err := tx.QueryRowContext(ctx, `SELECT mission_key FROM player_tiktok_mission_claims
			WHERE player_id=? AND mission_key=? FOR UPDATE`, playerID, key).Scan(&claimedKey)
		if err == nil {
			submission.alreadyClaimed = true
			result = append(result, submission)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("检查 TikTok 任务领取状态失败: %w", err)
		}

		if submission.legacyBundle {
			result = append(result, submission)
			continue
		}
		if submission.themeMutation == nil {
			complete, err := allLimitedThemeFragmentsOwned(ctx, tx, playerID)
			if err != nil {
				return nil, err
			}
			if !complete {
				return nil, player.ErrInvalidThemeMutation
			}
		} else {
			var owned int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_theme_fragments
				WHERE player_id=? AND theme_id=? AND fragment_index=?`, playerID,
				submission.themeMutation.ThemeID, submission.themeMutation.FragmentIndex).Scan(&owned); err != nil {
				return nil, fmt.Errorf("检查 TikTok 任务主题碎片失败: %w", err)
			}
			if owned != 0 {
				return nil, player.ErrInvalidThemeMutation
			}
		}
		result = append(result, submission)
	}
	return result, nil
}

func hasPropMutation(mutations []*player.PropMutation, identifier string) bool {
	for _, mutation := range mutations {
		if mutation.ID == identifier {
			return true
		}
	}
	return false
}

func allLimitedThemeFragmentsOwned(ctx context.Context, queryer queryContextRower, playerID uint64) (bool, error) {
	var count int
	err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM player_theme_fragments
		WHERE player_id=? AND theme_id IN (7,13,17,20,25,28,31,32,36,37,43,47,50,53,56,57,58)`, playerID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("统计 TikTok 任务限定主题碎片失败: %w", err)
	}
	return count == 17*player.LimitedThemeFragmentCount, nil
}

type execContexter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ensureSave(ctx context.Context, execer execContexter, playerID uint64) error {
	initial := player.InitialPropCounts()
	if _, err := execer.ExecContext(ctx, `INSERT INTO player_saves (player_id, hint_count, shuffle_count, remove_count, reached_level_at) VALUES (?, ?, ?, ?, UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE player_id = player_saves.player_id`,
		playerID, initial.Hint, initial.Shuffle, initial.Remove); err != nil {
		return fmt.Errorf("创建玩家默认存档失败: %w", err)
	}
	return nil
}

type queryContextRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func selectSave(ctx context.Context, queryer queryContextRower, playerID uint64) (player.Save, error) {
	save := player.Save{ClaimedTikTokMissions: make([]player.TikTokMissionKey, 0, 2)}
	err := queryer.QueryRowContext(ctx, `SELECT revision, level, selected_theme, collecting_theme, coins, hint_count, shuffle_count, remove_count, sound_enabled, music_enabled, effects_enabled, vibration_enabled, tutorial_completed, client_version, updated_at FROM player_saves WHERE player_id = ?`, playerID).
		Scan(&save.Revision, &save.Level, &save.SelectedTheme, &save.CollectingTheme, &save.Coins, &save.HintCount, &save.ShuffleCount, &save.RemoveCount, &save.SoundEnabled, &save.MusicEnabled, &save.EffectsEnabled, &save.VibrationEnabled, &save.TutorialCompleted, &save.ClientVersion, &save.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return player.Save{}, fmt.Errorf("玩家存档不存在")
	}
	if err != nil {
		return player.Save{}, fmt.Errorf("查询玩家存档失败: %w", err)
	}

	save.ThemeFragments = player.DefaultThemeFragments()
	rows, err := queryer.QueryContext(ctx, `SELECT theme_id, fragment_index FROM player_theme_fragments WHERE player_id = ? ORDER BY theme_id, fragment_index`, playerID)
	if err != nil {
		return player.Save{}, fmt.Errorf("查询主题碎片失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var themeID, fragmentIndex int
		if err := rows.Scan(&themeID, &fragmentIndex); err != nil {
			return player.Save{}, fmt.Errorf("读取主题碎片失败: %w", err)
		}
		if player.ValidThemeID(themeID) && fragmentIndex >= 0 && fragmentIndex < player.ThemeFragmentCount(themeID) {
			save.ThemeFragments[themeID] = append(save.ThemeFragments[themeID], fragmentIndex)
		}
	}
	if err := rows.Err(); err != nil {
		return player.Save{}, fmt.Errorf("遍历主题碎片失败: %w", err)
	}
	missionRows, err := queryer.QueryContext(ctx, `SELECT mission_key FROM player_tiktok_mission_claims
		WHERE player_id=? ORDER BY mission_key`, playerID)
	if err != nil {
		return player.Save{}, fmt.Errorf("查询 TikTok 任务领取状态失败: %w", err)
	}
	defer missionRows.Close()
	for missionRows.Next() {
		var key player.TikTokMissionKey
		if err := missionRows.Scan(&key); err != nil {
			return player.Save{}, fmt.Errorf("读取 TikTok 任务领取状态失败: %w", err)
		}
		if key == player.TikTokMissionHomeShortcut || key == player.TikTokMissionProfileRevisit {
			save.ClaimedTikTokMissions = append(save.ClaimedTikTokMissions, key)
		}
	}
	if err := missionRows.Err(); err != nil {
		return player.Save{}, fmt.Errorf("遍历 TikTok 任务领取状态失败: %w", err)
	}
	return save, nil
}

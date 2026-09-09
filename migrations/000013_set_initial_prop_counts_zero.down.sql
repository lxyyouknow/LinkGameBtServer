-- 回退只恢复列默认值，不修改任何玩家当前库存。
ALTER TABLE player_saves
    MODIFY hint_count INT UNSIGNED NOT NULL DEFAULT 3,
    MODIFY shuffle_count INT UNSIGNED NOT NULL DEFAULT 3,
    MODIFY remove_count INT UNSIGNED NOT NULL DEFAULT 3;

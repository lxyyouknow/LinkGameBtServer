-- 三列从建表起即为 NOT NULL，因此只调整新行默认值，不更新任何历史库存。
ALTER TABLE player_saves
    MODIFY hint_count INT UNSIGNED NOT NULL DEFAULT 0,
    MODIFY shuffle_count INT UNSIGNED NOT NULL DEFAULT 0,
    MODIFY remove_count INT UNSIGNED NOT NULL DEFAULT 0;

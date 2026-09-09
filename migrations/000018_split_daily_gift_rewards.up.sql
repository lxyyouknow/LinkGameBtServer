ALTER TABLE player_daily_gifts
    DROP CHECK chk_daily_gift_coins,
    DROP CHECK chk_daily_gift_prop,
    DROP PRIMARY KEY,
    ADD COLUMN reward_version TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER day_key,
    ADD COLUMN bonus_prop_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER prop_quantity,
    ADD COLUMN bonus_prop_quantity INT UNSIGNED NULL AFTER bonus_prop_type,
    ADD COLUMN bonus_theme_id TINYINT UNSIGNED NULL AFTER fragment_index,
    ADD COLUMN bonus_fragment_index TINYINT UNSIGNED NULL AFTER bonus_theme_id,
    ADD COLUMN bonus_claimed_at DATETIME(6) NULL AFTER claimed_at,
    ADD PRIMARY KEY (player_id, day_key, reward_version),
    ADD CONSTRAINT chk_daily_gift_v2_rewards CHECK (
        (reward_version = 1 AND coin_quantity = 50 AND prop_type IN ('hint','shuffle','remove') AND prop_quantity = 1)
        OR
        (reward_version = 2 AND coin_quantity = 0 AND prop_type = 'shuffle' AND prop_quantity = 1
            AND theme_id IS NOT NULL AND fragment_index IS NOT NULL
            AND bonus_prop_type = 'shuffle' AND bonus_prop_quantity = 1
            AND bonus_theme_id IS NOT NULL AND bonus_fragment_index IS NOT NULL
            AND NOT (theme_id = bonus_theme_id AND fragment_index = bonus_fragment_index))
    ),
    ADD CONSTRAINT chk_daily_gift_bonus_theme CHECK (bonus_theme_id IS NULL OR bonus_theme_id IN (7,13,17,20,25,28,31,32,36,37,43,47,50,53,56,57,58)),
    ADD CONSTRAINT chk_daily_gift_bonus_fragment CHECK (bonus_fragment_index IS NULL OR bonus_fragment_index <= 33),
    ADD CONSTRAINT chk_daily_gift_bonus_pair CHECK ((bonus_theme_id IS NULL AND bonus_fragment_index IS NULL) OR (bonus_theme_id IS NOT NULL AND bonus_fragment_index IS NOT NULL)),
    COMMENT = '每日礼包：V2 免费基础奖励与激励广告追加奖励分别幂等领取；V1 历史记录保留审计';

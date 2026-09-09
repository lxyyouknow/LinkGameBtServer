DELETE FROM player_daily_gifts WHERE reward_version = 2;

ALTER TABLE player_daily_gifts
    DROP CHECK chk_daily_gift_bonus_pair,
    DROP CHECK chk_daily_gift_bonus_fragment,
    DROP CHECK chk_daily_gift_bonus_theme,
    DROP CHECK chk_daily_gift_v2_rewards,
    DROP PRIMARY KEY,
    DROP COLUMN bonus_claimed_at,
    DROP COLUMN bonus_fragment_index,
    DROP COLUMN bonus_theme_id,
    DROP COLUMN bonus_prop_quantity,
    DROP COLUMN bonus_prop_type,
    DROP COLUMN reward_version,
    ADD PRIMARY KEY (player_id, day_key),
    ADD CONSTRAINT chk_daily_gift_prop CHECK (prop_type IN ('hint','shuffle','remove') AND prop_quantity = 1),
    ADD CONSTRAINT chk_daily_gift_coins CHECK (coin_quantity = 50),
    COMMENT = '每日激励广告礼包：随机道具 1 个与金币 50';

ALTER TABLE player_daily_gifts
    DROP CHECK chk_daily_gift_coins,
    DROP CHECK chk_daily_gift_prop,
    DROP COLUMN ad_session_id,
    DROP COLUMN coin_quantity,
    ADD CONSTRAINT chk_daily_gift_prop CHECK (prop_type = 'shuffle' AND prop_quantity = 1),
    COMMENT = '每日普通礼包；当前无广告版本不包含双倍奖励字段';

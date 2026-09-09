ALTER TABLE player_daily_gifts
    DROP CHECK chk_daily_gift_prop,
    ADD COLUMN coin_quantity BIGINT UNSIGNED NOT NULL DEFAULT 50 AFTER prop_quantity,
    ADD COLUMN ad_session_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER claim_request_id,
    ADD CONSTRAINT chk_daily_gift_prop CHECK (prop_type IN ('hint','shuffle','remove') AND prop_quantity = 1),
    ADD CONSTRAINT chk_daily_gift_coins CHECK (coin_quantity = 50),
    COMMENT = '每日激励广告礼包：随机道具 1 个与金币 50';

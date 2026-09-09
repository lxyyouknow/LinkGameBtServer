ALTER TABLE analytics_events
    ADD COLUMN ad_format VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER prop_type,
    ADD COLUMN ad_placement VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_format,
    ADD COLUMN ad_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_placement,
    ADD KEY idx_analytics_ad (app_id, sdk_type, channel, ad_format, ad_placement, received_at),
    ADD CONSTRAINT chk_analytics_ad_format CHECK (ad_format IS NULL OR ad_format IN ('rewarded','interstitial')),
    COMMENT = '小游戏原始幂等统计事件（含广告生命周期）';

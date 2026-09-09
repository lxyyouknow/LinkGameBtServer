ALTER TABLE analytics_events
    ADD COLUMN client_version VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER channel,
    ADD COLUMN platform VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER client_version,
    ADD COLUMN ad_attempt_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_placement,
    ADD COLUMN ad_error_stage VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_attempt_id,
    ADD COLUMN ad_sub_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_error_code,
    ADD COLUMN ad_result VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER ad_sub_error_code,
    ADD COLUMN ad_duration_ms INT UNSIGNED NULL AFTER ad_result,
    ADD COLUMN ad_preloaded BOOLEAN NULL AFTER ad_duration_ms,
    ADD KEY idx_analytics_ad_attempt (player_id, ad_attempt_id, received_at),
    ADD CONSTRAINT chk_analytics_platform CHECK (platform IS NULL OR platform IN ('web','tiktok')),
    ADD CONSTRAINT chk_analytics_ad_stage CHECK (ad_error_stage IS NULL OR ad_error_stage IN ('capability','create','load','show','play','close','reward_claim')),
    ADD CONSTRAINT chk_analytics_ad_result CHECK (ad_result IS NULL OR ad_result IN ('completed','early_closed','closed')),
    COMMENT = '小游戏原始幂等统计事件（含可串联的广告请求、加载、展示、关闭与奖励核销漏斗）';

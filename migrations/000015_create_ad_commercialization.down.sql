ALTER TABLE analytics_events
    DROP CHECK chk_analytics_ad_format,
    DROP KEY idx_analytics_ad,
    DROP COLUMN ad_error_code,
    DROP COLUMN ad_placement,
    DROP COLUMN ad_format,
    COMMENT = '无广告版原始幂等统计事件';

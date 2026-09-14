-- 2026-09-10：新增魔药一次性广告核销，不新增库存字段。
ALTER TABLE player_ad_sessions
    DROP CHECK chk_ad_session_placement,
    ADD CONSTRAINT chk_ad_session_placement CHECK (placement IN ('hint','shuffle','auto_remove','level_complete','daily_gift','daily_challenge_replay','season_makeup','potion'));

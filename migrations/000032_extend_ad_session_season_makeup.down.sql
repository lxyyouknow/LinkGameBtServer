ALTER TABLE player_ad_sessions
    DROP CHECK chk_ad_session_placement,
    DROP INDEX uk_ad_attempt,
    DROP COLUMN ad_attempt_id,
    ADD CONSTRAINT chk_ad_session_placement CHECK (placement IN ('hint','shuffle','auto_remove','level_complete','daily_gift','daily_challenge_replay'));

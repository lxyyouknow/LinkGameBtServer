ALTER TABLE player_ad_sessions
    DROP CHECK chk_ad_session_placement,
    ADD COLUMN ad_attempt_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER claim_request_id,
    ADD UNIQUE KEY uk_ad_attempt (player_id, ad_attempt_id),
    ADD CONSTRAINT chk_ad_session_placement CHECK (placement IN ('hint','shuffle','auto_remove','level_complete','daily_gift','daily_challenge_replay','season_makeup'));

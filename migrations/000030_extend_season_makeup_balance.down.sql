ALTER TABLE player_season_months
    DROP INDEX idx_season_makeup_recovery,
    DROP CHECK chk_season_makeup_remaining,
    DROP COLUMN makeup_recovery_day_key,
    DROP COLUMN makeup_remaining;

ALTER TABLE player_season_months
    ADD COLUMN makeup_remaining TINYINT UNSIGNED NOT NULL DEFAULT 5 AFTER state_version,
    ADD COLUMN makeup_recovery_day_key DATE NULL AFTER makeup_remaining,
    ADD CONSTRAINT chk_season_makeup_remaining CHECK (makeup_remaining <= 5),
    ADD KEY idx_season_makeup_recovery (season_key, makeup_recovery_day_key);

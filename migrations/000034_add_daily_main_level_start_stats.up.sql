ALTER TABLE analytics_daily_user_stats
    ADD COLUMN main_level_start_count INT UNSIGNED NOT NULL DEFAULT 0 AFTER enter_game_count;

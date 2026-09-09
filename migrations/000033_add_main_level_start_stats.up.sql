ALTER TABLE analytics_player_stats
    ADD COLUMN main_level_start_count INT UNSIGNED NOT NULL DEFAULT 0 AFTER enter_game_count;

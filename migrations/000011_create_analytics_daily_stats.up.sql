CREATE TABLE analytics_daily_user_stats (
    stat_date DATE NOT NULL,
    player_id BIGINT UNSIGNED NOT NULL,
    app_id INT UNSIGNED NOT NULL DEFAULT 1,
    sdk_type INT NOT NULL DEFAULT 0,
    channel INT NOT NULL DEFAULT 0,
    login_count INT UNSIGNED NOT NULL DEFAULT 0,
    enter_game_count INT UNSIGNED NOT NULL DEFAULT 0,
    online_duration BIGINT UNSIGNED NOT NULL DEFAULT 0,
    current_level INT UNSIGNED NOT NULL DEFAULT 0,
    level_pass_count INT UNSIGNED NOT NULL DEFAULT 0,
    theme_select_count INT UNSIGNED NOT NULL DEFAULT 0,
    prop_use_count INT UNSIGNED NOT NULL DEFAULT 0,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (stat_date, player_id),
    KEY idx_analytics_daily_filter (app_id, sdk_type, channel, stat_date),
    CONSTRAINT fk_analytics_daily_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='无广告版每日玩家统计';

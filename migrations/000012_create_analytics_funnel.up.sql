CREATE TABLE analytics_funnel_events (
    player_id BIGINT UNSIGNED NOT NULL,
    app_id INT UNSIGNED NOT NULL DEFAULT 1,
    sdk_type INT NOT NULL DEFAULT 0,
    channel INT NOT NULL DEFAULT 0,
    step VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    level INT UNSIGNED NOT NULL,
    first_occurred_at DATETIME(6) NOT NULL,
    PRIMARY KEY (player_id, step, level),
    KEY idx_analytics_funnel_filter (app_id, sdk_type, channel, first_occurred_at, step, level),
    CONSTRAINT fk_analytics_funnel_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='关卡漏斗首次到达记录';

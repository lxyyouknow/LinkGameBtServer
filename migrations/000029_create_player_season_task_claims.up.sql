CREATE TABLE player_season_task_claims (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL,
    request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    task VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    season_key CHAR(7) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    day_key DATE NOT NULL,
    result JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_season_task_request (player_id, request_id),
    KEY idx_season_task_day (player_id, day_key, task),
    CONSTRAINT fk_season_task_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_season_task CHECK (task IN ('login','clear_levels'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='赛季任务领取完整幂等响应';

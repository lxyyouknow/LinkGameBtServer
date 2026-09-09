CREATE TABLE player_season_clear_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL,
    request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    completion_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    level INT UNSIGNED NULL,
    day_key DATE NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_season_clear_request (player_id, request_id),
    UNIQUE KEY uk_season_clear_completion (player_id, completion_id),
    KEY idx_season_clear_day (player_id, day_key, created_at),
    CONSTRAINT fk_season_clear_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_season_clear_source CHECK (source IN ('main','daily')),
    CONSTRAINT chk_season_clear_level CHECK (level IS NULL OR level <= 999)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='赛季成功结算幂等事件';

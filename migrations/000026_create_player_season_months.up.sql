CREATE TABLE player_season_months (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL,
    season_key CHAR(7) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    config_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    config_id TINYINT UNSIGNED NOT NULL,
    state_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_player_season_month (player_id, season_key),
    KEY idx_season_month_key (season_key, updated_at),
    CONSTRAINT fk_season_month_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_season_config_id CHECK (config_id <= 11),
    CONSTRAINT chk_season_state_version CHECK (state_version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家自然月赛季权威状态';

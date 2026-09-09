CREATE TABLE player_theme_fragments (
    player_id BIGINT UNSIGNED NOT NULL,
    theme_id TINYINT UNSIGNED NOT NULL,
    fragment_index TINYINT UNSIGNED NOT NULL,
    acquired_at DATETIME(6) NOT NULL,
    PRIMARY KEY (player_id, theme_id, fragment_index),
    KEY idx_theme_fragment_theme (theme_id, acquired_at),
    CONSTRAINT fk_theme_fragment_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_theme_fragment_theme CHECK (theme_id BETWEEN 1 AND 58),
    CONSTRAINT chk_theme_fragment_index CHECK (fragment_index <= 33)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家已获得的主题碎片；主题 0 默认完整不入表';

CREATE TABLE player_sessions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL COMMENT '所属玩家',
    token_hash BINARY(32) NOT NULL COMMENT '会话 Token 的 SHA-256，不保存明文 Token',
    expires_at DATETIME(6) NOT NULL COMMENT '会话过期时间',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_used_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    revoked_at DATETIME(6) NULL COMMENT '非空表示会话已撤销',
    PRIMARY KEY (id),
    UNIQUE KEY uk_player_sessions_token_hash (token_hash),
    KEY idx_player_sessions_player_id (player_id),
    KEY idx_player_sessions_expires_at (expires_at),
    CONSTRAINT fk_player_sessions_player
        FOREIGN KEY (player_id) REFERENCES players (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家登录会话';

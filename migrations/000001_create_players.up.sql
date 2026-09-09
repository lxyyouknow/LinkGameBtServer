CREATE TABLE players (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '数据库内部玩家 ID',
    public_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '对客户端公开的不可枚举 ID',
    status VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active' COMMENT '账号状态',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    last_login_at DATETIME(6) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_players_public_id (public_id),
    CONSTRAINT chk_players_status CHECK (status IN ('active', 'disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家主账号';

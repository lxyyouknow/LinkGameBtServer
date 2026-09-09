CREATE TABLE player_public_profiles (
    player_number BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '排行榜稳定公开玩家编号',
    player_id BIGINT UNSIGNED NOT NULL COMMENT '所属玩家',
    authorization_status VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'unknown',
    display_name VARCHAR(64) NULL COMMENT 'TikTok 明确授权后的展示昵称',
    avatar_url VARCHAR(2048) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT 'TikTok 明确授权后的 HTTPS 头像地址',
    profile_updated_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (player_number),
    UNIQUE KEY uk_player_public_profiles_player (player_id),
    CONSTRAINT fk_player_public_profiles_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_player_public_profiles_status CHECK (authorization_status IN ('unknown', 'authorized', 'denied', 'revoked'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='排行榜公开编号与授权资料';

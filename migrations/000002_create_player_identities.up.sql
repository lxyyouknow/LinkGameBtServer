CREATE TABLE player_identities (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL COMMENT '所属玩家',
    provider VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'guest、douyin、wechat 等身份来源',
    provider_uid VARCHAR(191) COLLATE utf8mb4_0900_bin NOT NULL COMMENT '来源内唯一身份 ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_player_identities_provider_uid (provider, provider_uid),
    KEY idx_player_identities_player_id (player_id),
    CONSTRAINT fk_player_identities_player
        FOREIGN KEY (player_id) REFERENCES players (id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家登录身份';

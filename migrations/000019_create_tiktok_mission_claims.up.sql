CREATE TABLE IF NOT EXISTS player_tiktok_mission_claims (
    player_id BIGINT UNSIGNED NOT NULL,
    mission_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    prop_mutation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    theme_mutation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    claimed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (player_id, mission_key),
    KEY idx_tiktok_mission_claimed_at (claimed_at),
    CONSTRAINT fk_tiktok_mission_claim_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_tiktok_mission_key CHECK (mission_key IN ('home_shortcut:v1','profile_revisit:v1'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='TikTok 添加桌面与 Profile 侧边栏任务的跨设备幂等领取状态';

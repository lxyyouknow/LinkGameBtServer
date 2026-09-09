CREATE TABLE player_daily_challenge_states (
    player_id BIGINT UNSIGNED NOT NULL,
    challenge_level INT UNSIGNED NOT NULL DEFAULT 0,
    day_key DATE NOT NULL,
    completion_count INT UNSIGNED NOT NULL DEFAULT 0,
    replay_available BOOLEAN NOT NULL DEFAULT FALSE,
    active_attempt_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (player_id),
    KEY idx_daily_challenge_day (day_key, updated_at),
    CONSTRAINT fk_daily_challenge_state_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT chk_daily_challenge_state_replay CHECK (replay_available IN (0,1)),
    CONSTRAINT chk_daily_challenge_state_version CHECK (version >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='玩家每日挑战绝对进度、东京自然日完成次数与广告再挑战资格';

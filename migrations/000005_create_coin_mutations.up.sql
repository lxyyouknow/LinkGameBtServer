CREATE TABLE player_coin_mutations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    player_id BIGINT UNSIGNED NOT NULL,
    mutation_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    delta BIGINT NOT NULL,
    reason VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    client_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_coin_mutation (player_id, mutation_id),
    KEY idx_coin_mutation_player_created (player_id, created_at),
    CONSTRAINT fk_coin_mutation_player FOREIGN KEY (player_id) REFERENCES players (id) ON UPDATE RESTRICT ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='金币幂等流水';

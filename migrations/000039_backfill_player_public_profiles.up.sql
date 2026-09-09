INSERT IGNORE INTO player_public_profiles (player_id) SELECT id FROM players ORDER BY id;

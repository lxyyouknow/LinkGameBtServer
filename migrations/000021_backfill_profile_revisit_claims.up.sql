INSERT IGNORE INTO player_tiktok_mission_claims (player_id, mission_key, claimed_at)
SELECT player_id, 'profile_revisit:v1', MIN(created_at)
FROM (
    SELECT player_id, created_at FROM player_coin_mutations
    WHERE mutation_id = 'tiktok:profile_revisit:v1:coin'
    UNION ALL
    SELECT player_id, created_at FROM player_prop_mutations
    WHERE mutation_id IN (
        'tiktok:profile_revisit:v1:hint',
        'tiktok:profile_revisit:v1:shuffle',
        'tiktok:profile_revisit:v1:remove'
    )
    UNION ALL
    SELECT player_id, created_at FROM player_theme_mutations
    WHERE mutation_id = 'tiktok:profile_revisit:v1:theme'
) AS historical_profile_revisit
GROUP BY player_id;

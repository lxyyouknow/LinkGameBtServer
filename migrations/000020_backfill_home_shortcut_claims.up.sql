INSERT IGNORE INTO player_tiktok_mission_claims (player_id, mission_key, claimed_at)
SELECT player_id, 'home_shortcut:v1', MIN(created_at)
FROM (
    SELECT player_id, created_at FROM player_coin_mutations
    WHERE mutation_id = 'tiktok:home_shortcut:v1:coin'
    UNION ALL
    SELECT player_id, created_at FROM player_prop_mutations
    WHERE mutation_id IN (
        'tiktok:home_shortcut:v1:hint',
        'tiktok:home_shortcut:v1:shuffle',
        'tiktok:home_shortcut:v1:hint-v2'
    )
    UNION ALL
    SELECT player_id, created_at FROM player_theme_mutations
    WHERE mutation_id = 'tiktok:home_shortcut:v1:theme-v2'
) AS historical_home_shortcut
GROUP BY player_id;

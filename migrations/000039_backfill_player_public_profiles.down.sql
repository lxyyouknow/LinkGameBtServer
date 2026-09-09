DELETE FROM player_public_profiles WHERE authorization_status = 'unknown' AND display_name IS NULL AND avatar_url IS NULL;

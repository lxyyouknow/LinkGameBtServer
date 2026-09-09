-- 已入库的旧 enter_game 只在点击主线开始时触发，可准确回填旧版开始数据。
UPDATE analytics_player_stats
SET main_level_start_count = enter_game_count;

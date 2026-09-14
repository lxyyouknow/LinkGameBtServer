-- 2026-09-10：只调整新行默认值，不重置或补发历史玩家库存。
ALTER TABLE player_saves
    ALTER COLUMN hint_count SET DEFAULT 0,
    ALTER COLUMN shuffle_count SET DEFAULT 0,
    ALTER COLUMN remove_count SET DEFAULT 0;

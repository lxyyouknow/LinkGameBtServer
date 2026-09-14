-- 2026-09-10：魔药是使用统计维度，不新增可持久化的经济道具类型。
ALTER TABLE analytics_events
    DROP CHECK chk_analytics_event_prop,
    ADD CONSTRAINT chk_analytics_event_prop CHECK (prop_type IS NULL OR prop_type IN ('hint','shuffle','remove','potion'));

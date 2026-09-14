ALTER TABLE analytics_events
    DROP CHECK chk_analytics_event_prop,
    ADD CONSTRAINT chk_analytics_event_prop CHECK (prop_type IS NULL OR prop_type IN ('hint','shuffle','remove'));

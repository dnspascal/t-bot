ALTER TABLE market_states ADD COLUMN IF NOT EXISTS prev_day_high NUMERIC(12,5);
ALTER TABLE market_states ADD COLUMN IF NOT EXISTS prev_day_low  NUMERIC(12,5);

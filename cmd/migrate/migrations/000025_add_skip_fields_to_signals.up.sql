ALTER TABLE signals ADD COLUMN IF NOT EXISTS skip_reason    TEXT;
ALTER TABLE signals ADD COLUMN IF NOT EXISTS intended_entry NUMERIC(12,5);
ALTER TABLE signals ADD COLUMN IF NOT EXISTS intended_sl    NUMERIC(12,5);
ALTER TABLE signals ADD COLUMN IF NOT EXISTS intended_tp    NUMERIC(12,5);

CREATE INDEX IF NOT EXISTS idx_signals_skipped ON signals (skip_reason, created_at DESC) WHERE skip_reason IS NOT NULL;

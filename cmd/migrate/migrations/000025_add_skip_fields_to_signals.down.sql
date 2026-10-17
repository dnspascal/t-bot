DROP INDEX IF EXISTS idx_signals_skipped;
ALTER TABLE signals DROP COLUMN IF EXISTS intended_tp;
ALTER TABLE signals DROP COLUMN IF EXISTS intended_sl;
ALTER TABLE signals DROP COLUMN IF EXISTS intended_entry;
ALTER TABLE signals DROP COLUMN IF EXISTS skip_reason;

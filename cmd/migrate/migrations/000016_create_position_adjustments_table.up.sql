CREATE TABLE position_adjustments (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    position_id     UUID            NOT NULL REFERENCES positions(id) ON DELETE CASCADE,

    old_sl          NUMERIC(12,5),  
    new_sl          NUMERIC(12,5),  
    old_tp          NUMERIC(12,5),  
    new_tp          NUMERIC(12,5),  

    reason          TEXT            NOT NULL,  

    adjusted_at     TIMESTAMPTZ     DEFAULT NOW()
);

CREATE INDEX idx_position_adjustments_position ON position_adjustments(position_id);
CREATE INDEX idx_position_adjustments_reason ON position_adjustments(reason, adjusted_at DESC);

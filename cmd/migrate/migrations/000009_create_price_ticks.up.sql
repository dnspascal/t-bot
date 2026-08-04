CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE price_ticks (
    id               UUID          NOT NULL DEFAULT gen_random_uuid(),
    symbol_id        UUID          NOT NULL REFERENCES symbols(id),
    bid              NUMERIC(12,5) NOT NULL,
    ask              NUMERIC(12,5) NOT NULL,
    mid              NUMERIC(12,5) NOT NULL GENERATED ALWAYS AS ((bid + ask) / 2) STORED,
    spread           NUMERIC(8,5)  NOT NULL GENERATED ALWAYS AS (ask - bid) STORED,
    session_close    NUMERIC(12,5),           
    provider_timestamp TIMESTAMPTZ,           
    received_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(), 
    processing_us    BIGINT        NOT NULL DEFAULT 0,     
    PRIMARY KEY (id, received_at)
);

SELECT create_hypertable('price_ticks', 'received_at');

CREATE INDEX idx_price_ticks_symbol_id ON price_ticks (symbol_id, received_at DESC);

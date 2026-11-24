-- Rebuilt from data/raw by load.sh. One row per candle per download; CSVs are bid OHLCV, ts = bar open (Unix s, UTC).
CREATE OR REPLACE TABLE candles AS
SELECT
  regexp_extract(filename, 'raw/([^/]+)/[^/]+/[^/]+/', 1)          AS source,
  regexp_extract(filename, 'raw/[^/]+/([^/]+)/', 1)                AS symbol,
  regexp_extract(filename, 'raw/[^/]+/[^/]+/([^/]+)/', 1)          AS download,
  regexp_extract(filename, '([^/]+)\.csv$', 1)                     AS tf,
  (to_timestamp(ts) AT TIME ZONE 'UTC')                            AS ts,
  (to_timestamp(ts) AT TIME ZONE 'Africa/Nairobi')                 AS ts_eat,
  o, h, l, c, v
FROM read_csv('data/raw/*/*/*/*.csv',
  header = false, filename = true,
  columns = {'ts': 'BIGINT', 'o': 'DOUBLE', 'h': 'DOUBLE', 'l': 'DOUBLE', 'c': 'DOUBLE', 'v': 'DOUBLE'})
WHERE filename NOT LIKE '%.partial/%';

CREATE OR REPLACE VIEW downloads AS
SELECT source, symbol, download, tf, count(*) AS rows, min(ts) AS first, max(ts) AS last
FROM candles GROUP BY ALL ORDER BY symbol, download, tf;

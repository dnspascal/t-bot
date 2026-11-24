CREATE TEMP TABLE pc AS SELECT period AS tf, to_timestamp(ts) AT TIME ZONE 'UTC' AS ts, open o, high h, low l, close c, tick_volume v, lag_s FROM read_csv('data/prod/candles.tsv', delim='\t', header=true);
CREATE TEMP TABLE d AS SELECT tf, ts, o, h, l, c, v FROM candles WHERE symbol='XAUUSD' AND download='2026-11-24_from2021_copy2' AND ts >= TIMESTAMP '2026-07-02';
CREATE TEMP TABLE j AS SELECT pc.tf, pc.ts, pc.o po, pc.h ph, pc.l pl, pc.c pcl, pc.v pv, d.o, d.h, d.l, d.c, d.v, d.ts IS NOT NULL AS hit
FROM pc LEFT JOIN d ON d.tf=pc.tf AND d.ts=pc.ts WHERE pc.ts <= TIMESTAMP '2026-11-24 06:00';
SELECT tf, count(*) AS prod_bars, count(*) FILTER (hit) AS in_history,
  count(*) FILTER (po=o AND ph=h AND pl=l AND pcl=c) AS ohlc_exact,
  count(*) FILTER (po=o) AS open_eq, count(*) FILTER (ph=h) AS high_eq, count(*) FILTER (pl=l) AS low_eq, count(*) FILTER (pcl=c) AS close_eq,
  round(quantile_cont(abs(pcl-c), 0.99), 3) AS p99_close_diff, round(max(abs(pcl-c)), 3) AS max_close_diff,
  count(*) FILTER (pv=v) AS vol_eq
FROM j GROUP BY 1 ORDER BY 1;

-- Does a quiet morning (00-12 UTC = 03-15 EAT) predict an afternoon (12-21 UTC = 15-24 EAT) that stays in its range?
CREATE TEMP TABLE m5 AS SELECT to_timestamp(column0) AT TIME ZONE 'UTC' ts, column2 h, column3 l, column4 c
FROM read_csv('data/raw/ctrader/XAUUSD/2026-11-24_from2016/M5.csv', header=false);
CREATE TEMP TABLE d AS
SELECT ts::DATE AS day,
  max(h) FILTER (hour(ts) < 12) mh, min(l) FILTER (hour(ts) < 12) ml,
  max(h) FILTER (hour(ts) BETWEEN 12 AND 20) ah, min(l) FILTER (hour(ts) BETWEEN 12 AND 20) al,
  arg_max(c, ts) FILTER (hour(ts) < 12) m_close, max(h) - min(l) AS day_range
FROM m5 WHERE dayofweek(ts) BETWEEN 1 AND 5 GROUP BY 1 HAVING count(*) FILTER (hour(ts) < 12) > 100 AND count(*) FILTER (hour(ts) BETWEEN 12 AND 20) > 80;
CREATE TEMP TABLE q AS
SELECT *, mh - ml AS mr,
  avg(day_range) OVER (ORDER BY day ROWS BETWEEN 20 PRECEDING AND 1 PRECEDING) AS adr20,
  (mh - ml) / avg(day_range) OVER (ORDER BY day ROWS BETWEEN 20 PRECEDING AND 1 PRECEDING) AS mr_vs_adr
FROM d;
CREATE TEMP TABLE o AS
SELECT *, CASE WHEN mr_vs_adr < 0.35 THEN '1 very quiet (<35% of ADR)' WHEN mr_vs_adr < 0.5 THEN '2 quiet (35-50%)'
               WHEN mr_vs_adr < 0.7 THEN '3 normal (50-70%)' ELSE '4 busy (70%+)' END AS morning,
  ah <= mh AND al >= ml AS contained,
  ah > mh AND al < ml AS both_sides,
  greatest(ah - mh, ml - al, 0) / mr AS ext_in_mr
FROM q WHERE adr20 IS NOT NULL;

.print '== afternoon vs morning range, by how quiet the morning was (2016-2026)'
SELECT morning, count(*) AS days,
  round(100.0*avg(contained::INT),0) AS stays_inside_pct,
  round(100.0*avg((ah > mh AND al >= ml OR al < ml AND ah <= mh)::INT),0) AS breaks_one_side_pct,
  round(100.0*avg(both_sides::INT),0) AS breaks_both_pct,
  round(median(ext_in_mr) FILTER (NOT contained),2) AS median_break_x_morning_range,
  round(100.0*avg((ext_in_mr >= 0.5)::INT),0) AS breaks_by_half_range_or_more_pct
FROM o GROUP BY 1 ORDER BY 1;

.print '== very quiet + quiet mornings, by year: % of afternoons that stay inside'
SELECT year(day) yr, count(*) AS quiet_days, round(100.0*avg(contained::INT),0) AS stays_inside_pct,
  round(100.0*avg((ext_in_mr >= 0.5)::INT),0) AS breaks_half_range_pct
FROM o WHERE mr_vs_adr < 0.5 GROUP BY 1 ORDER BY 1;

-- Gold day-of-week and month effects, 2016-2026, from broker D1 bars (re-open to close, no swap inside a day).
CREATE TEMP TABLE d AS
SELECT to_timestamp(column0) AT TIME ZONE 'UTC' AS t, column1 o, column4 c
FROM read_csv('data/raw/ctrader/XAUUSD/2026-11-24_from2016/D1.csv', header=false);
-- a D1 bar opening Sunday 22:00/23:00 UTC is Monday's session
CREATE TEMP TABLE s AS
SELECT (t + INTERVAL 3 HOUR)::DATE AS session, dayname(t + INTERVAL 3 HOUR) AS wd, year(t + INTERVAL 3 HOUR) AS yr,
       o, c, (c - o) / o * 1e4 AS bp, c - o AS usd
FROM d WHERE dayofweek(t + INTERVAL 3 HOUR) BETWEEN 1 AND 5;
CREATE TEMP TABLE m AS
SELECT *, row_number() OVER (PARTITION BY yr, month(session) ORDER BY session) AS tdom,
          row_number() OVER (PARTITION BY yr, month(session) ORDER BY session DESC) AS tdom_back
FROM s;

.print '== weekday: average session move, t-stat, years positive, and a buy-every-<day> trade at $0.20 per oz'
WITH y AS (SELECT wd, yr, avg(bp) ybp FROM s GROUP BY ALL)
SELECT s.wd, count(*) AS n, round(avg(bp),2) AS mean_bp, round(avg(bp)/(stddev(bp)/sqrt(count(*))),2) AS t,
  round(100.0*avg((bp>0)::INT)) AS up_pct,
  (SELECT count(*) FROM y WHERE y.wd=s.wd AND ybp>0) AS yrs_up,
  round(sum(usd) - 0.20*count(*)) AS buy_net_usd, round(-sum(usd) - 0.20*count(*)) AS sell_net_usd
FROM s GROUP BY s.wd ORDER BY list_position(['Monday','Tuesday','Wednesday','Thursday','Friday'], s.wd);

.print '== turn of the month: last trading day + first 3 vs the rest'
SELECT CASE WHEN tdom_back = 1 OR tdom <= 3 THEN 'turn of month (last day + first 3)' ELSE 'rest of month' END AS part,
  count(*) n, round(avg(bp),2) mean_bp, round(avg(bp)/(stddev(bp)/sqrt(count(*))),2) t,
  round(sum(usd) - 0.20*count(*)) AS buy_net_usd
FROM m GROUP BY 1;
WITH y AS (SELECT yr, avg(bp) FILTER (tdom_back = 1 OR tdom <= 3) tom, avg(bp) FILTER (NOT (tdom_back = 1 OR tdom <= 3)) rest FROM m GROUP BY yr)
SELECT 'years turn-of-month beat rest' AS check, count(*) FILTER (tom > rest) AS yes, count(*) AS of FROM y;

.print '== month of year: average daily move (bp) and years the month was up'
WITH mo AS (SELECT yr, month(session) mth, sum(bp) mbp FROM s GROUP BY ALL)
SELECT mth, round(avg(mbp),0) AS avg_month_bp, count(*) FILTER (mbp > 0) AS yrs_up, count(*) AS yrs FROM mo GROUP BY mth ORDER BY mth;

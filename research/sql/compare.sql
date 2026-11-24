-- Compares downloads A and B candle by candle over their shared time range. Set with:
--   SET VARIABLE a = '<download>'; SET VARIABLE b = '<download>'; SET VARIABLE sym = 'XAUUSD';
WITH a AS (SELECT * FROM candles WHERE symbol = getvariable('sym') AND download = getvariable('a')),
     b AS (SELECT * FROM candles WHERE symbol = getvariable('sym') AND download = getvariable('b')),
     span AS (SELECT a.tf, greatest(min(a.ts), (SELECT min(ts) FROM b WHERE b.tf = a.tf)) AS lo,
                           least(max(a.ts), (SELECT max(ts) FROM b WHERE b.tf = a.tf)) AS hi
              FROM a GROUP BY a.tf),
     j AS (SELECT coalesce(a.tf, b.tf) AS tf, a.ts AS ta, b.ts AS tb,
                  greatest(abs(a.o - b.o), abs(a.h - b.h), abs(a.l - b.l), abs(a.c - b.c)) AS diff, a.v - b.v AS dv
           FROM a FULL JOIN b ON a.tf = b.tf AND a.ts = b.ts
           JOIN span s ON s.tf = coalesce(a.tf, b.tf) AND coalesce(a.ts, b.ts) BETWEEN s.lo AND s.hi)
SELECT tf,
       count(*) FILTER (ta IS NOT NULL AND tb IS NOT NULL)              AS both,
       count(*) FILTER (tb IS NULL)                                     AS only_a,
       count(*) FILTER (ta IS NULL)                                     AS only_b,
       count(*) FILTER (diff = 0 AND dv = 0)                            AS identical,
       count(*) FILTER (diff > 0)                                       AS price_differs,
       round(max(diff), 5)                                              AS max_price_diff,
       count(*) FILTER (dv <> 0)                                        AS volume_differs
FROM j GROUP BY tf ORDER BY tf;

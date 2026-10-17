-- Entry signals the bot recorded but deliberately did not send (signals.skip_reason, migration 000025).
-- intended_entry/sl/tp are the prices the order would have used: replay them against candles (M1)
-- to see what each skipped trade would have done.

-- Every skipped signal, newest first
SELECT s.created_at AT TIME ZONE 'Africa/Dar_es_Salaam' AS at_eat,
       sym.symbol, s.strategy, s.signal AS side, s.skip_reason,
       s.intended_entry, s.intended_sl, s.intended_tp, s.reason
FROM signals s
JOIN symbols sym ON sym.id = s.symbol_id
WHERE s.skip_reason IS NOT NULL
ORDER BY s.created_at DESC;

-- Count per day, reason and strategy
SELECT (s.created_at AT TIME ZONE 'Africa/Dar_es_Salaam')::date AS day_eat,
       s.skip_reason, s.strategy, COUNT(*) AS skipped
FROM signals s
WHERE s.skip_reason IS NOT NULL
GROUP BY 1, 2, 3
ORDER BY 1 DESC, 4 DESC;

# Research

Market data research for t-bot. Code, SQL and study write-ups are committed; `data/` and `bin/` are git-ignored
(the repo is public — never commit candles).

```
cmd/download/   Go: downloads gold 6y (copy), gold 10y, oil 10y in parallel with live progress, into
                data/raw/ctrader/<SYMBOL>/<date>_from<year>[_label]/ + manifest.json (rows, first/last, sha256)
scripts/  build.sh     build the backtester (analysis/backtest) into bin/bt
          load.sh      rebuild data/research.duckdb from every download
          compare.sh   compare two downloads candle by candle
sql/      schema.sql   candles table + downloads view
          compare.sql
studies/  one folder per study: README.md (question, method, result) + queries
data/     raw downloads (never edited after download) and research.duckdb
```

## Data

- cTrader trendbars M1, M5, M15, M30, H1, H4, D1: bid prices, bar open time in Unix seconds UTC, no header: `ts,o,h,l,c,v`.
- The smallest cTrader trendbar is M1; second-level data would need tick downloads.
- A download folder is never overwritten; downloading again creates a new dated folder, so copies can be compared.

## Download

Credentials come from the shell environment only (`CTRADER_CLIENT_ID`, `CTRADER_CLIENT_SECRET`,
`CTRADER_ACCESS_TOKEN`, `CTRADER_ACCOUNT_ID`, `CTRADER_DEMO`).

```
go run ./research/cmd/download research/data     # oil symbol name: oilSymbol in cmd/download/download.go
research/scripts/load.sh
```

## Query

```
duckdb -readonly research/data/research.duckdb
SELECT * FROM downloads;
SELECT ts_eat, o, h, l, c FROM candles WHERE symbol='XAUUSD' AND tf='H1' AND download='2026-11-17_from2021' LIMIT 10;
research/scripts/compare.sh XAUUSD 2026-11-17_from2021 2026-11-24_from2021_copy2
```

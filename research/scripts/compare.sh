#!/usr/bin/env bash
# Usage: compare.sh <SYMBOL> <download A> <download B>   (download = folder name under data/raw/ctrader/<SYMBOL>/)
set -euo pipefail
R="$(cd "$(dirname "$0")/.." && pwd)"
DUCKDB="${DUCKDB:-$(command -v duckdb || echo "$HOME/bin/duckdb")}"
cd "$R"
{ echo "SET VARIABLE sym = '$1'; SET VARIABLE a = '$2'; SET VARIABLE b = '$3';"; cat sql/compare.sql; } \
  | "$DUCKDB" -readonly data/research.duckdb

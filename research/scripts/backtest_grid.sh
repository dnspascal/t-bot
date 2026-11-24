#!/usr/bin/env bash
# Usage: backtest_grid.sh <data dir> <out dir> <spread> <pip> <strategy list file> [parallel]
# Runs every strategy with the live exit settings, one <strategy>_trades.csv per strategy in <out dir>.
set -euo pipefail
R="$(cd "$(dirname "$0")/.." && pwd)"
data="$1" out="$2" spread="$3" pip="$4" list="$5" par="${6:-6}"
mkdir -p "$out"
export BT_PD=off BT_LOCK=50:10,75:60,90:70 BT_INVAL=m5 BT_INVAL_RULE=erm2 BT_PIP="$pip" BT_TRADES="$out" BT_NOSNAP=1
xargs -P "$par" -n 1 sh -c '"$0" run "$1" "$3" "$2" > /dev/null 2>&1 || echo "failed: $3"' "$R/bin/bt" "$data" "$spread" < <(cat "$list") \
  | sed "s|^|[$spread] |" || true

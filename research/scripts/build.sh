#!/usr/bin/env bash
# Builds the downloader/backtester into research/bin.
set -euo pipefail
R="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$R/bin"
(cd "$R/.." && go build -o "$R/bin/bt" ./analysis/backtest)

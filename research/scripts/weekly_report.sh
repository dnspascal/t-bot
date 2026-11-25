#!/usr/bin/env bash
# Usage: weekly_report.sh [days]. Opens the read-only prod tunnel (.claude/prod-db.sh, local only) and writes
# research/data/reports/weekly_<date>.md (git-ignored: it holds the account balance and P&L).
set -euo pipefail
R="$(cd "$(dirname "$0")/.." && pwd)"
cd "$R/.."
url="$(mktemp)"
trap 'rm -f "$url"' EXIT
.claude/prod-db.sh "$url" > /dev/null
mkdir -p "$R/data/reports"
out="$R/data/reports/weekly_$(date +%F).md"
go run ./research/cmd/report -url-file "$url" -days "${1:-7}" -out "$out"
echo "report: $out"

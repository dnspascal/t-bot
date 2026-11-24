#!/usr/bin/env bash
# Rebuilds data/research.duckdb from every download under data/raw.
set -euo pipefail
R="$(cd "$(dirname "$0")/.." && pwd)"
DUCKDB="${DUCKDB:-$(command -v duckdb || echo "$HOME/bin/duckdb")}"
cd "$R"
rm -f data/research.duckdb
"$DUCKDB" data/research.duckdb < sql/schema.sql
"$DUCKDB" data/research.duckdb -c "SELECT * FROM downloads"

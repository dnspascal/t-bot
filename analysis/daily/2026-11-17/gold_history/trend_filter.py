"""Splits backtest trades by whether they agree with gold's N-day trend (close now vs N days ago, from H1).
Usage: python3 -I trend_filter.py <H1.csv> <strategy_trades.csv>
"""
import bisect, csv, datetime as dt, sys
from collections import defaultdict

csv.field_size_limit(10**9)
h1 = [(int(r[0]), float(r[4])) for r in csv.reader(open(sys.argv[1]))]
ts = [t for t, _ in h1]


def close_at(t):
    i = bisect.bisect_right(ts, t) - 1
    return h1[i][1] if i >= 0 else None


trades = [(r[0], int(r[1]), float(r[4])) for r in csv.reader(open(sys.argv[2]))]
for days in (10, 20, 30, 60, 90):
    groups = defaultdict(lambda: defaultdict(list))
    for side, t, R in trades:
        now, past = close_at(t - 3600), close_at(t - days * 86400)
        if past is None:
            continue
        up = now > past
        key = "with trend" if (side == "BUY") == up else "against trend"
        groups[key][dt.datetime.fromtimestamp(t, dt.timezone.utc).year].append(R)
    for key in ("with trend", "against trend"):
        by = groups[key]
        allR = [x for v in by.values() for x in v]
        print(f"{days:3}d {key:14} n={len(allR):4} totalR={sum(allR):+7.1f} avgR={sum(allR) / len(allR):+.3f} yrs+ {sum(sum(v) > 0 for v in by.values())}/{len(by)} | "
              + "  ".join(f"{y}:{sum(v):+6.1f}" for y, v in sorted(by.items())))
    print()

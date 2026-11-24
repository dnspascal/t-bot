"""Scorecard for a grid of backtest variants (one <name>_trades.csv per variant, '_'-separated parameters in the name).
Checks: years positive (5 of every 6), total at double spread, neighbour plateau (variants differing in one parameter),
in-sample first half vs out-of-sample second half, trade count, last-year median stop in price units.
Usage: python3 -I scorecard.py <dir at normal spread> <dir at double spread> [top N] [first year] [max stop]
Defaults match the gold study: 2021, max stop 20 ($ at 1 oz). Use a large max stop to skip that check.
"""
import csv
import datetime as dt
import glob
import os
import statistics as st
import sys
from collections import defaultdict

d1, d2 = sys.argv[1], sys.argv[2]
top = int(sys.argv[3]) if len(sys.argv) > 3 else 15
FIRST = int(sys.argv[4]) if len(sys.argv) > 4 else 2021
MAXSTOP = float(sys.argv[5]) if len(sys.argv) > 5 else 20
YEARS = range(FIRST, 2027)
HALF = FIRST + len(YEARS) // 2
IS_YEARS, OOS_YEARS = range(FIRST, HALF), range(HALF, 2027)
NEED = -(-5 * len(YEARS) // 6)


def load(path):
    rows = [r for r in csv.reader(open(path)) if r and r[0] in ("BUY", "SELL")]
    by = defaultdict(float)
    stops = []
    for r in rows:
        y = dt.datetime.fromtimestamp(int(r[1]), dt.timezone.utc).year
        by[y] += float(r[4])
        if y == 2026:
            stops.append(abs(float(r[7]) - float(r[8])))
    return rows, by, stops


res = {}
for p in glob.glob(f"{d1}/*_trades.csv"):
    name = os.path.basename(p)[:-len("_trades.csv")]
    rows, by, stops = load(p)
    if not rows:
        continue
    p2 = f"{d2}/{name}_trades.csv"
    tot2 = sum(load(p2)[1].values()) if os.path.exists(p2) else float("nan")
    res[name] = dict(n=len(rows), tot=sum(by.values()), by=by, tot2=tot2, stop=st.median(stops) if stops else 0,
                     win=100 * sum(float(r[4]) > 0 for r in rows) / len(rows),
                     ins=sum(by[y] for y in IS_YEARS), oos=sum(by[y] for y in OOS_YEARS))

parts = {n: n.split("_") for n in res}
for n, r in res.items():
    nb = [res[m]["tot"] for m in res if m != n and len(parts[m]) == len(parts[n])
          and sum(a != b for a, b in zip(parts[m], parts[n])) == 1]
    r["plateau"] = st.median(nb) if nb else float("nan")
    r["ypos"] = sum(r["by"][y] > 0 for y in YEARS)
    r["pass"] = r["ypos"] >= NEED and r["tot2"] > 0 and r["n"] >= 300 and r["plateau"] > 0 and r["stop"] <= MAXSTOP

hdr = f"{'variant':34}{'n':>6}{'win%':>5}{'total R':>8}{'2x spr':>8}{'plateau':>8}{f'IS {IS_YEARS[0]%100}-{IS_YEARS[-1]%100}':>9}{f'OOS {OOS_YEARS[0]%100}-{OOS_YEARS[-1]%100}':>10}{'yrs+':>5}{'stop':>8}  " + " ".join(f"{y:>6}" for y in YEARS)
line = lambda n, r: (f"{n:34}{r['n']:6}{r['win']:5.0f}{r['tot']:8.1f}{r['tot2']:8.1f}{r['plateau']:8.1f}{r['ins']:9.1f}{r['oos']:10.1f}"
                     f"{r['ypos']:5}{r['stop']:8.1f}  " + " ".join(f"{r['by'][y]:6.1f}" for y in YEARS) + ("  PASS" if r["pass"] else ""))
print(f"{len(res)} variants; all-variant median total {st.median(r['tot'] for r in res.values()):.1f}R, "
      f"{sum(r['tot'] > 0 for r in res.values())} positive, {sum(r['pass'] for r in res.values())} pass every check\n")
print(f"checks: years positive >= {NEED} of {len(YEARS)}, 2x spread > 0, plateau > 0, n >= 300, {YEARS[-1]} median stop <= {MAXSTOP}\n")
print(f"top by total R (whole {len(YEARS)} years):")
print(hdr)
for n, r in sorted(res.items(), key=lambda kv: -kv[1]["tot"])[:top]:
    print(line(n, r))
print(f"\nout-of-sample check — chosen only by {IS_YEARS[0]}-{IS_YEARS[-1]}, then judged on {OOS_YEARS[0]}-{OOS_YEARS[-1]}:")
print(hdr)
for n, r in sorted(res.items(), key=lambda kv: -kv[1]["ins"])[:top]:
    print(line(n, r))
print(f"\nchosen by in-sample: top-{top} median OOS {st.median(r['oos'] for _, r in sorted(res.items(), key=lambda kv: -kv[1]['ins'])[:top]):.1f}R "
      f"vs all-variant median OOS {st.median(r['oos'] for r in res.values()):.1f}R")
print("\nby parameter value (median total R across all variants with that value):")
k = max(len(p) for p in parts.values())
for i in range(1, k):
    vals = defaultdict(list)
    for n, r in res.items():
        if len(parts[n]) == k:
            vals[parts[n][i]].append(r["tot"])
    if len(vals) > 1:
        print(f"  param {i}: " + "  ".join(f"{v}={st.median(x):+.1f}" for v, x in sorted(vals.items())))

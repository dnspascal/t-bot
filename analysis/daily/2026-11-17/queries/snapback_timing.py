"""When do trades turn against us? Walks every live XAUUSD trade's M1 path from open to close and
marks swings: a favourable peak counts once price then retraces THR dollars from it (and vice versa).
Usage: python3 -I snapback_timing.py <m1.csv> <trades.tsv> [threshold_dollars]
"""
import bisect, csv, statistics as st, sys
from collections import defaultdict

THR = float(sys.argv[3]) if len(sys.argv) > 3 else 3.0
m1 = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(sys.argv[1]))]
ts = [b[0] for b in m1]


def ticks(i0, i1):
    """Approximate intrabar order: up bars go open-low-high-close, down bars open-high-low-close."""
    for t, o, h, l, c in m1[i0:i1]:
        for p in ((o, l, h, c) if c >= o else (o, h, l, c)):
            yield t, p


def swings(t0, t1, side, entry):
    """Zigzag from the fill: a leg counts once price moves THR from the last extreme. Returns (pivots, first_leg)
    where pivots are (minute_from_open, gain, 'peak'|'trough') and first_leg is 'up' (our way), 'down' or None."""
    sgn = 1 if side == "BUY" else -1
    i0, i1 = bisect.bisect_left(ts, t0 // 60 * 60), bisect.bisect_right(ts, t1)
    piv, mode, first = [], None, None
    hi = lo = 0.0
    hi_t = lo_t = t0
    for t, p in ticks(i0, i1):
        t = max(t, t0)
        g = sgn * (p - entry)
        if mode is None:
            if g > hi:
                hi, hi_t = g, t
            if g < lo:
                lo, lo_t = g, t
            if g - lo >= THR:
                mode = first = "up"
                hi, hi_t = g, t
            elif hi - g >= THR:
                mode = first = "down"
                lo, lo_t = g, t
        elif mode == "up":
            if g > hi:
                hi, hi_t = g, t
            if hi - g >= THR:
                piv.append(((hi_t - t0) / 60, hi, "peak"))
                mode, lo, lo_t = "down", g, t
        else:
            if g < lo:
                lo, lo_t = g, t
            if g - lo >= THR:
                piv.append(((lo_t - t0) / 60, lo, "trough"))
                mode, hi, hi_t = "up", g, t
    return piv, first


def q(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))] if xs else float("nan")


groups = defaultdict(list)
for r in csv.reader(open(sys.argv[2]), delimiter="\t"):
    t0, t1, strat, side, entry, pnl, reason = int(r[0]), int(r[1]), r[2], r[3], float(r[4]), float(r[7]), r[8]
    if t1 <= t0:
        continue
    piv, first = swings(t0, t1, side, entry)
    res = dict(dur=(t1 - t0) / 60, piv=piv, first=first, pnl=pnl, sl=reason.startswith("sl_hit"), tp=reason.startswith("tp_hit"))
    for k in ("ALL", strat):
        groups[k].append(res)

print(f"Swing = price moves at least ${THR:.0f} the other way from its last extreme. Times are minutes after the fill.\n")
print(f"{'strategy':20}{'n':>5}{'our way first':>14}{'against first':>14}{'neither':>8}{'1st peak min (median)':>22}{'p25-p75':>10}"
      f"{'1st peak $ (median)':>20}{'turns per trade (median)':>25}{'avg':>6}{'trade min':>10}")
for k in ["ALL"] + sorted(x for x in groups if x != "ALL"):
    g = groups[k]
    if len(g) < 20:
        continue
    first_way = [x for x in g if x["first"] == "up" and x["piv"] and x["piv"][0][2] == "peak"]
    t_first = [x["piv"][0][0] for x in first_way]
    gain_first = [x["piv"][0][1] for x in first_way]
    turns = [sum(1 for p in x["piv"] if p[2] == "peak") for x in g]
    up = 100 * sum(x["first"] == "up" for x in g) / len(g)
    dn = 100 * sum(x["first"] == "down" for x in g) / len(g)
    print(f"{k:20}{len(g):5}{up:13.0f}%{dn:13.0f}%{100 - up - dn:7.0f}%{q(t_first, .5):22.0f}{q(t_first, .25):5.0f}-{q(t_first, .75):<4.0f}"
          f"{q(gain_first, .5):20.2f}{q(turns, .5):25.0f}{st.mean(turns):6.1f}{q([x['dur'] for x in g], .5):10.0f}")

g = groups["ALL"]
print("\nFirst turn, ALL trades that went our way first — how long until it turned against us:")
first = [x["piv"][0][0] for x in g if x["first"] == "up" and x["piv"] and x["piv"][0][2] == "peak"]
for lo, hi in ((0, 2), (2, 5), (5, 10), (10, 15), (15, 30), (30, 60), (60, 120), (120, 10**9)):
    n = sum(lo <= v < hi for v in first)
    print(f"  {lo:>3}-{hi if hi < 10**9 else '':<4} min  {n:4}  {100 * n / len(first):4.0f}%  " + "#" * round(60 * n / len(first)))
print("\nTurns against us per trade (ALL), and how those trades ended:")
for n_turns in range(0, 8):
    sel = [x for x in g if sum(1 for p in x["piv"] if p[2] == "peak") == n_turns] if n_turns < 7 else [x for x in g if sum(1 for p in x["piv"] if p[2] == "peak") >= 7]
    if sel:
        print(f"  {str(n_turns) + ('+' if n_turns == 7 else ''):>3} turns  {len(sel):4} trades  SL {100 * sum(x['sl'] for x in sel) / len(sel):3.0f}%  TP {100 * sum(x['tp'] for x in sel) / len(sel):3.0f}%  "
              f"avg ${st.mean(x['pnl'] for x in sel):6.2f}  median trade {q([x['dur'] for x in sel], .5):4.0f} min")

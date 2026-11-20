"""Live invalidation checks 3-of-4 reversal signals on M5 bar close only. Compares that against (a) the stored M1
states checked every minute and (b) the same M5 indicators recomputed every minute with the forming bar's price.
Rest of the exit stack as live (SL/TP/breakeven/peak drawback on M1 high-low, stall, never-profitable, EOD).
Market exits fill at the next M1 open.
Usage: python3 -I invalidation_speed.py <positions.tsv> <m1.tsv> <m5_full.tsv> <m1_states.tsv> <m1_open.tsv>
"""
import bisect
import csv
import datetime as dt
import sys
from collections import Counter, defaultdict

BUF = 0.5
NO_WATCHER = {"ema_pullback", "sr_bounce", "session_momentum", "dd_oversold_bounce", "rsi_reversal"}
rd = lambda f: list(csv.reader(open(f), delimiter="\t"))[1:]
pos = list(csv.DictReader(open(sys.argv[1]), delimiter="\t"))
m1 = [(int(r[0]), float(r[1]), float(r[2])) for r in rd(sys.argv[2])]
m1t = [b[0] for b in m1]
m1hl = {b[0] + 60: (b[1], b[2]) for b in m1}

m5r = rd(sys.argv[3])
m5t = [int(r[0]) for r in m5r]
m5c = [float(r[3]) for r in m5r]
m5h = [float(r[1]) for r in m5r]
m5l = [float(r[2]) for r in m5r]
G, L = [0.0] * len(m5c), [0.0] * len(m5c)
for i in range(1, len(m5c)):
    d = m5c[i] - m5c[i - 1]
    gi, li = max(d, 0), max(-d, 0)
    G[i], L[i] = ((G[i-1] * 13 + gi) / 14, (L[i-1] * 13 + li) / 14) if i > 14 else (G[i-1] + gi / 14, L[i-1] + li / 14)
EF = [float(r[6]) for r in m5r]
ES = [float(r[7]) for r in m5r]

# signal tuple: (regime, rsi, ema_fast, ema_slow, momentum)
sig = {"m5 close (live)": {}, "m1 states": {}, "m5 every minute": {}}
for i, r in enumerate(m5r):
    sig["m5 close (live)"][m5t[i]] = (r[4], float(r[5]), EF[i], ES[i], r[8])
m1close = {}
nxt = {int(r[0]): float(r[1]) for r in rd(sys.argv[5])}
for r in rd(sys.argv[4]):
    t, c = int(r[0]), float(r[1])
    m1close[t] = c
    sig["m1 states"][t] = (r[2], float(r[3]), float(r[4]), float(r[5]), r[6])
    k = bisect.bisect_right(m5t, t) - 1
    if k < 25:
        continue
    if m5t[k] == t:
        sig["m5 every minute"][t] = sig["m5 close (live)"][t]
        continue
    d = c - m5c[k]
    g, l = (G[k] * 13 + max(d, 0)) / 14, (L[k] * 13 + max(-d, 0)) / 14
    rsi = 100 - 100 / (1 + g / l) if l else 100
    ef, es = EF[k] + 0.2 * (c - EF[k]), ES[k] + (2 / 22) * (c - ES[k])
    hs = [m1hl[x][0] for x in range(m5t[k] + 60, t + 1, 60) if x in m1hl] or [c]
    ls = [m1hl[x][1] for x in range(m5t[k] + 60, t + 1, 60) if x in m1hl] or [c]
    hi, lo = max(hs), min(ls)
    if hi > max(m5h[k - 19:k + 1]) or lo < min(m5l[k - 19:k + 1]):
        reg = "breakout"
    elif abs(ef - es) / ((ef + es) / 2) < 0.001:
        reg = "ranging"
    else:
        reg = "trending_up" if ef > es else "trending_down"
    p3 = m5c[k - 2]
    mom = "rising" if rsi > 60 and c > p3 else ("falling" if rsi < 40 and c < p3 else "stable")
    sig["m5 every minute"][t] = (reg, rsi, ef, es, mom)


def against(st, s):
    reg, rsi, ef, es, mom = st
    return ((reg in (("trending_down", "ranging") if s > 0 else ("trending_up", "ranging")))
            + ((rsi < 50) if s > 0 else (rsi > 50)) + ((ef < es) if s > 0 else (ef > es))
            + (mom == ("falling" if s > 0 else "rising")))


def eod(t):
    d = dt.datetime.fromtimestamp(t, dt.timezone.utc)
    return d.weekday() < 5 and d.hour == 21 and d.minute >= 30


def sim(p, mode, need=3):
    s = 1 if p["side"] == "BUY" else -1
    op, sl, tp = float(p["open_price"]), float(p["sl"]), float(p["tp"])
    g = lambda x: s * (x - op)
    tpd, stop, peak, arm = g(tp), g(sl), 0.0, g(tp) / 2
    ot = int(p["ot"])
    watch = p["strat"] not in NO_WATCHER
    table = sig[mode]
    i = bisect.bisect_left(m1t, ot - ot % 60)
    for t, h, l in m1[i:i + 60 * 24 * 7]:
        if eod(t):
            return "eod", g(nxt.get(t + 60, (h + l) / 2)), t
        adv, fav = (l, h) if s > 0 else (h, l)
        if g(adv) <= stop:
            return ("SL" if stop == g(sl) else "breakeven"), stop, t
        if g(fav) >= tpd:
            return "TP", tpd, t
        if peak >= arm and g(adv) <= peak * 0.25:
            return "peak drawback", peak * 0.25, t
        peak = max(peak, g(fav))
        if peak >= arm:
            stop = max(stop, BUF)
            if g(adv) <= peak * 0.25:
                return "peak drawback", peak * 0.25, t
        age = t + 60 - ot
        if 1800 <= age < 1860 and peak <= 0.1:
            return "never-profitable", g(nxt.get(t + 60, (h + l) / 2)), t
        if 3600 <= age < 3660 and peak < 0.15 * tpd:
            return "stall", g(nxt.get(t + 60, (h + l) / 2)), t
        st = table.get(t + 60)
        if watch and st and t + 60 > ot + 60 and against(st, s) >= need:
            return "invalidation", g(nxt.get(t + 60, (h + l) / 2)), t
    return "open", 0.0, 0


modes = [("m5 close (live)", 3), ("m5 every minute", 3), ("m1 states", 3), ("m1 states", 4)]
res = {f"{m} {n}/4": [sim(p, m, n) for p in pos] for m, n in modes}
base = res["m5 close (live) 3/4"]
w = [i for i, p in enumerate(pos) if p["strat"] not in NO_WATCHER]
print(f"trades {len(pos)}, {len(w)} use invalidation\n")
print(f"{'invalidation check':24}{'total $/oz':>11}{'vs live':>9}{'inval exits':>12}{'avg inval exit':>16}   by month (watcher strategies only)")
for k, rs in res.items():
    tot = sum(r[1] for r in rs)
    iv = [r[1] for r in rs if r[0] == "invalidation"]
    bym = defaultdict(float)
    for i in w:
        bym[dt.datetime.fromtimestamp(int(pos[i]["ot"]), dt.timezone.utc).strftime("%m")] += rs[i][1]
    print(f"{k:24}{tot:11.2f}{tot-sum(r[1] for r in base):9.2f}{len(iv):12}{sum(iv)/len(iv):16.2f}   " + " ".join(f"{m}:{v:7.1f}" for m, v in sorted(bym.items())))

for k in list(res)[1:]:
    print(f"\nlive M5 invalidation exits vs '{k}':")
    a = [(b, x) for b, x in zip(base, res[k]) if b[0] == "invalidation"]
    earlier = [(b, x) for b, x in a if x[0] == "invalidation" and x[2] < b[2]]
    if earlier:
        print(f"  fires earlier on {len(earlier)}/{len(a)}: median {sorted((b[2]-x[2])/60 for b, x in earlier)[len(earlier)//2]:.0f} min sooner, "
              f"exit better by {sum(x[1]-b[1] for b, x in earlier):.2f} total ({sum(x[1]-b[1] for b, x in earlier)/len(earlier):.2f}/trade)")
    extra = [(b, x) for b, x in zip(base, res[k]) if b[0] != "invalidation" and x[0] == "invalidation"]
    c = defaultdict(lambda: [0, 0.0, 0.0])
    for b, x in extra:
        c[b[0]][0] += 1
        c[b[0]][1] += b[1]
        c[b[0]][2] += x[1]
    print(f"  new closes on trades live did NOT invalidate: {len(extra)}")
    for r_, (n, before, after) in sorted(c.items(), key=lambda kv: -kv[1][0]):
        print(f"    were {r_:16}{n:4}: live {before:8.2f} -> {after:8.2f}  ({after-before:+.2f})")

print("\nby strategy:")
print(f"{'strategy':20}" + "".join(f"{k:>22}" for k in res))
for st in sorted({pos[i]['strat'] for i in w}):
    idx = [i for i in w if pos[i]["strat"] == st]
    print(f"{st:20}" + "".join(f"{sum(res[k][i][1] for i in idx):22.2f}" for k in res))

"""How trend_follow trades look 15-60 min after entry, chop vs clean-trend (hindsight label), and what an early-exit
rule decided only from what is known at that minute would have done. Counterfactual = close at that M1 close; the
trade's actual R is used otherwise. Sells pay the spread.
Usage: python3 -I tf_early_exit.py <gold-history dir> <tfx_trades.csv> [spread]
"""
import bisect
import csv
import datetime as dt
import sys
from collections import defaultdict

d, tf = sys.argv[1], sys.argv[2]
SPREAD = float(sys.argv[3]) if len(sys.argv) > 3 else 0.30
h1 = [(int(r[0]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
atr_t, atr_v, a, pc = [], [], None, None
for t, hi, lo, c in h1:
    tr = hi - lo if pc is None else max(hi - lo, abs(hi - pc), abs(lo - pc))
    a = tr if a is None else (a * 13 + tr) / 14
    pc = c
    atr_t.append(t + 3600)
    atr_v.append(a)
atr_at = lambda t: atr_v[max(bisect.bisect_right(atr_t, t) - 1, 0)]
m5 = [(int(r[0]), float(r[2]), float(r[3])) for r in csv.reader(open(f"{d}/M5.csv"))]
legs, direc = [], 0
for t, hi, lo in m5:
    if direc == 0:
        direc, ext, ext_t, start, start_t, sa = 1, hi, t, lo, t, atr_at(t)
        continue
    thr = atr_at(ext_t)
    if direc > 0:
        if hi > ext:
            ext, ext_t = hi, t
        elif ext - lo >= thr:
            legs.append((start_t, ext_t, 1, start, ext, sa))
            direc, start, start_t, sa, ext, ext_t = -1, ext, ext_t, atr_at(ext_t), lo, t
    else:
        if lo < ext:
            ext, ext_t = lo, t
        elif hi - ext >= thr:
            legs.append((start_t, ext_t, -1, start, ext, sa))
            direc, start, start_t, sa, ext, ext_t = 1, ext, ext_t, atr_at(ext_t), hi, t
clean = [l for l in legs if abs(l[4] - l[3]) >= 3 * l[5]]
starts = [l[0] for l in clean]
del m5

m1 = [(int(r[0]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/M1.csv"))]
m1t = [b[0] for b in m1]
CK = (15, 30, 45, 60)
trades = []
for r in csv.reader(open(tf)):
    if r[0] not in ("BUY", "SELL"):
        continue
    side, o, c, R, entry, sl = r[0], int(r[1]), int(r[2]), float(r[4]), float(r[7]), float(r[8])
    s = 1 if side == "BUY" else -1
    risk = abs(entry - sl)
    if risk <= 0:
        continue
    i = bisect.bisect_right(starts, o) - 1
    lab = "chop"
    if i >= 0 and clean[i][0] <= o <= clean[i][1]:
        lab = "with" if (clean[i][2] > 0) == (s > 0) else "against"
    j = bisect.bisect_left(m1t, o)
    mfe = mae = 0.0
    snaps = {}
    for t, hi, lo, cl in m1[j:j + 61]:
        age = (t + 60 - o) // 60
        if t + 60 > c:
            break
        fav, adv = (hi, lo) if s > 0 else (lo + SPREAD, hi + SPREAD)
        mfe, mae = max(mfe, s * (fav - entry) / risk), min(mae, s * (adv - entry) / risk)
        if age in CK:
            snaps[age] = (mfe, mae, s * ((cl if s > 0 else cl + SPREAD) - entry) / risk)
    trades.append(dict(lab=lab, R=R, snaps=snaps, y=dt.datetime.fromtimestamp(o, dt.timezone.utc).year))

base = sum(t["R"] for t in trades)
print(f"{len(trades)} trades, base total {base:.1f}R\n")
print("still-open trades at each checkpoint: average best-so-far (MFE) / worst-so-far (MAE) / current, in R")
for T in CK:
    line = f"  {T:2} min:"
    for lab in ("with", "chop", "against"):
        s = [t["snaps"][T] for t in trades if t["lab"] == lab and T in t["snaps"]]
        line += f"   {lab} n={len(s):4} MFE {sum(x[0] for x in s)/len(s):+.2f} MAE {sum(x[1] for x in s)/len(s):+.2f} now {sum(x[2] for x in s)/len(s):+.2f}"
    print(line)

print("\nrule: at T minutes, close at market if the trade's best so far (MFE) is below x R")
print(f"{'rule':24}{'closed':>7}{'of them chop/with':>19}{'new total R':>12}{'change':>8}")
best = []
for T in CK:
    for x in (0.1, 0.2, 0.3, 0.4, 0.5):
        hit = [t for t in trades if T in t["snaps"] and t["snaps"][T][0] < x]
        delta = sum(t["snaps"][T][2] - t["R"] for t in hit)
        nc, nw = sum(t["lab"] == "chop" for t in hit), sum(t["lab"] == "with" for t in hit)
        best.append((delta, T, x))
        print(f"  T={T:2} MFE<{x:.1f}R        {len(hit):7}{nc:>10}/{nw:<8}{base+delta:12.1f}{delta:+8.1f}")
print("\nrule: at T minutes, close at market if the trade is currently below y R")
for T in CK:
    for y in (-0.5, -0.3, -0.1, 0.0):
        hit = [t for t in trades if T in t["snaps"] and t["snaps"][T][2] < y]
        delta = sum(t["snaps"][T][2] - t["R"] for t in hit)
        nc, nw = sum(t["lab"] == "chop" for t in hit), sum(t["lab"] == "with" for t in hit)
        print(f"  T={T:2} now<{y:+.1f}R        {len(hit):7}{nc:>10}/{nw:<8}{base+delta:12.1f}{delta:+8.1f}")

print("\nbest candidates, change in R by year (needs opened time):")
for T, x in ((30, 0.2), (30, 0.3), (45, 0.4), (45, 0.5), (60, 0.4), (60, 0.5)):
    by = defaultdict(float)
    for t in trades:
        if T in t["snaps"] and t["snaps"][T][0] < x:
            by[t["y"]] += t["snaps"][T][2] - t["R"]
    ys = sorted({t["y"] for t in trades})
    print(f"  T={T} MFE<{x}R: " + " ".join(f"{y}:{by[y]:+5.1f}" for y in ys) + f"   better in {sum(by[y]>0 for y in ys)}/{len(ys)} years")

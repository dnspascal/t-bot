"""Summarises tfx backtest variants against clean gold trend legs (zigzag 1 H1 ATR reversal, 3+ ATR).
Usage: python3 -I tf_variant_summary.py <gold-history dir> <variants dir> <name>...  (each <variants dir>/<name>/tfx_trades.csv)
"""
import bisect
import csv
import datetime as dt
import sys
from collections import defaultdict

d, vd, names = sys.argv[1], sys.argv[2], sys.argv[3:]
h1 = [(int(r[0]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
atr_t, atr_v, a, pc = [], [], None, None
for t, hi, lo, c in h1:
    tr = hi - lo if pc is None else max(hi - lo, abs(hi - pc), abs(lo - pc))
    a = tr if a is None else (a * 13 + tr) / 14
    pc = c
    atr_t.append(t + 3600)
    atr_v.append(a)
m5 = [(int(r[0]), float(r[2]), float(r[3])) for r in csv.reader(open(f"{d}/M5.csv"))]
m5t = [b[0] for b in m5]
atr_at = lambda t: atr_v[max(bisect.bisect_right(atr_t, t) - 1, 0)]
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
halves = []
for s_t, e_t, dr, sp, ep, _ in clean:
    half, ht = sp + (ep - sp) / 2, e_t
    for t, hi, lo in m5[bisect.bisect_left(m5t, s_t):bisect.bisect_right(m5t, e_t)]:
        if dr > 0 and hi >= half or dr < 0 and lo <= half:
            ht = t
            break
    halves.append(ht)

print(f"{'variant':16}{'trades':>7}{'win%':>6}{'total R':>9}{'with R':>8}{'chop R':>8}{'chop n':>7}{'legs 1st half':>14}   " + " ".join(f"{y:>6}" for y in range(2021, 2027)))
for n in names:
    tr = [r for r in csv.reader(open(f"{vd}/{n}/tfx_trades.csv")) if r[0] in ("BUY", "SELL")]
    lab = defaultdict(list)
    ent = defaultdict(list)
    py = defaultdict(float)
    for r in tr:
        o, R = int(r[1]), float(r[4])
        ent[r[0]].append(o)
        py[dt.datetime.fromtimestamp(o, dt.timezone.utc).year] += R
        i = bisect.bisect_right(starts, o) - 1
        k = "chop"
        if i >= 0 and clean[i][0] <= o <= clean[i][1]:
            k = "with" if (clean[i][2] > 0) == (r[0] == "BUY") else "against"
        lab[k].append(R)
    for v in ent.values():
        v.sort()
    caught = 0
    for l, ht in zip(clean, halves):
        es = ent["BUY" if l[2] > 0 else "SELL"]
        k = bisect.bisect_left(es, l[0])
        caught += k < len(es) and es[k] <= ht
    Rs = [float(r[4]) for r in tr]
    print(f"{n:16}{len(tr):7}{100*sum(x>0 for x in Rs)/len(Rs):6.0f}{sum(Rs):9.1f}{sum(lab['with']):8.1f}{sum(lab['chop']):8.1f}{len(lab['chop']):7}"
          f"{100*caught/len(clean):13.0f}%   " + " ".join(f"{py[y]:6.1f}" for y in range(2021, 2027)))

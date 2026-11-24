"""trend_follow vs clean trend legs (zigzag, 1 H1 ATR reversal, 3+ ATR = clean).
A: entry features of trades opened in chop vs inside a clean leg, and what each candidate filter keeps/removes.
B: clean legs trend_follow never entered in their first half, and which of its rules blocked them.
Usage: python3 -I tf_chop_and_missed.py <gold-history dir> <tfx_trades.csv> <tfx log.csv>
"""
import bisect
import csv
import datetime as dt
import sys
from collections import Counter, defaultdict

d, trades_f, log_f = sys.argv[1:4]
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

log = {int(r["t"]): r for r in csv.DictReader(open(log_f))}
tr = [r for r in csv.reader(open(trades_f)) if r[0] in ("BUY", "SELL")]


def label(o, side):
    i = bisect.bisect_right(starts, o) - 1
    if i >= 0 and clean[i][0] <= o <= clean[i][1]:
        return "with" if (clean[i][2] > 0) == (side == "BUY") else "against"
    return "chop"


rows = []
for r in tr:
    o = int(r[1])
    f = log.get(o) or log.get(o - 300) or log.get(o + 300)
    if not f:
        continue
    rows.append(dict(o=o, side=r[0], R=float(r[4]), lab=label(o, r[0]), adx=float(f["h1_adx"]), gap=float(f["gap_ratio"]),
                     bounce=float(f["bounce_atr"]), er=float(f["er12"]), atrx=float(f["atrx"]), e200=f["e200"] == "1",
                     hour=int(f["hour"])))
print(f"A. trend_follow 6y trades matched to entry features: {len(rows)} of {len(tr)}")
for lab in ("with", "chop", "against"):
    rs = [x for x in rows if x["lab"] == lab]
    avg = lambda k: sum(x[k] for x in rs) / len(rs)
    print(f"  {lab:8} n={len(rs):5} R={sum(x['R'] for x in rs):7.1f}  H1 ADX {avg('adx'):5.1f}  gap {avg('gap'):.2f}  bounce {avg('bounce'):.2f}"
          f"  ER12 {avg('er'):.2f}  ATRx {avg('atrx'):.2f}  above/below EMA200 {100*avg('e200'):3.0f}%")
tot = sum(x["R"] for x in rows)
print(f"\n  candidate filter (hindsight-free, uses entry values only): trades kept / R kept / R removed / chop share removed")
cands = []
for k, ths, ge in (("adx", (25, 30, 35, 40), True), ("er", (0.2, 0.3, 0.4, 0.5), True), ("atrx", (0.9, 1.0, 1.1, 1.2), True),
                   ("gap", (0.7, 0.8, 0.9), True), ("bounce", (0.3, 0.2, 0.1), False)):
    for th in ths:
        keep = [x for x in rows if (x[k] >= th if ge else x[k] < th)]
        cands.append((f"{k} {'>=' if ge else '<'} {th}", keep))
cands.append(("price beyond H1 EMA200", [x for x in rows if x["e200"]]))
for h in ((9, 20), (10, 20), (14, 20), (15, 19)):
    cands.append((f"EAT hour {h[0]}-{h[1]}", [x for x in rows if h[0] <= x["hour"] < h[1]]))
nchop = sum(x["lab"] == "chop" for x in rows)
for name, keep in cands:
    kr = sum(x["R"] for x in keep)
    rem_chop = nchop - sum(x["lab"] == "chop" for x in keep)
    print(f"  {name:26} kept {len(keep):5}  R kept {kr:7.1f}  R removed {tot-kr:7.1f}  chop trades removed {100*rem_chop/nchop:3.0f}%"
          f"  good trades removed {100*(1-sum(x['lab']=='with' for x in keep)/sum(x['lab']=='with' for x in rows)):3.0f}%")

print("\nB. clean legs (3+ ATR) and trend_follow")
entries = defaultdict(list)
for r in tr:
    entries[r[0]].append(int(r[1]))
for v in entries.values():
    v.sort()
rule_cols = ["h1_regime", "m5_ema", "m5_regime", "fresh", "adx", "ema50", "gap", "bounce", "cooldown"]
missed, caught, first_half_end = [], [], {}
for l in clean:
    s_t, e_t, dr, sp, ep, _ = l
    half = sp + (ep - sp) / 2
    i, j = bisect.bisect_left(m5t, s_t), bisect.bisect_right(m5t, e_t)
    ht = e_t
    for t, hi, lo in m5[i:j]:
        if dr > 0 and hi >= half or dr < 0 and lo <= half:
            ht = t
            break
    side = "BUY" if dr > 0 else "SELL"
    es = entries[side]
    k = bisect.bisect_left(es, s_t)
    (caught if k < len(es) and es[k] <= ht else missed).append((l, ht))
print(f"  {len(clean)} clean legs; trend_follow entered with the leg in its first half on {len(caught)} ({100*len(caught)/len(clean):.0f}%), missed {len(missed)}")
sole, anyfail, wrongdir, nbars = Counter(), Counter(), 0, 0
leg_block = Counter()
for (s_t, e_t, dr, sp, ep, _), ht in missed:
    side = "BUY" if dr > 0 else "SELL"
    per_leg = Counter()
    for t in range(s_t - s_t % 300 + 300, ht + 300, 300):
        f = log.get(t)
        if not f:
            continue
        nbars += 1
        if f["dir"] != side:
            wrongdir += 1
            per_leg["H1 direction not with the leg" if f["dir"] else "H1 ranging"] += 1
            continue
        fails = [c for c in rule_cols if f[c] == "0"]
        for c in fails:
            anyfail[c] += 1
        if len(fails) == 1:
            sole[fails[0]] += 1
            per_leg["only " + fails[0]] += 1
    if per_leg:
        leg_block[per_leg.most_common(1)[0][0]] += 1
print(f"  first-half M5 bars of missed legs: {nbars}; H1 direction wrong or ranging on {wrongdir} ({100*wrongdir/max(nbars,1):.0f}%)")
print("  when H1 direction was right, each rule failing (bars):", dict(anyfail.most_common()))
print("  bars where exactly ONE rule blocked the entry:", dict(sole.most_common()))
print("  missed legs by their main blocker:", dict(leg_block.most_common()))

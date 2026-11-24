"""How often gold ranges (H1 ADX) and whether range edges hold after a false break, 2016-2026.
Event: an M5 bar pokes beyond the range edge and closes back inside (first per edge per hour). Outcome: which comes first,
price back at the range middle (fade works) or 0.5 H1 ATR beyond the poke extreme (range breaks).
Usage: python3 -I range_edges.py <download dir with H1.csv and M5.csv>
"""
import bisect, csv, datetime as dt, sys
from collections import defaultdict

d = sys.argv[1]
h1 = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
m5 = [(int(r[0]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/M5.csv"))]

# Wilder ATR and ADX(14) on H1, value known at bar close
n = 14
atr = adx = pdm_s = mdm_s = None
ind = []
for i, (t, o, hi, lo, c) in enumerate(h1):
    if i == 0:
        ind.append((t + 3600, None, None)); continue
    _, _, phi, plo, pc = h1[i - 1]
    tr = max(hi - lo, abs(hi - pc), abs(lo - pc))
    up, dn = hi - phi, plo - lo
    pdm, mdm = (up if up > dn and up > 0 else 0), (dn if dn > up and dn > 0 else 0)
    if atr is None:
        atr, pdm_s, mdm_s = tr, pdm, mdm
    else:
        atr, pdm_s, mdm_s = atr + (tr - atr) / n, pdm_s + (pdm - pdm_s) / n, mdm_s + (mdm - mdm_s) / n
    pdi, mdi = 100 * pdm_s / atr if atr else 0, 100 * mdm_s / atr if atr else 0
    dx = 100 * abs(pdi - mdi) / (pdi + mdi) if pdi + mdi else 0
    adx = dx if adx is None else adx + (dx - adx) / n
    ind.append((t + 3600, atr, adx if i > 3 * n else None))
it = [x[0] for x in ind]

def state(t):
    i = bisect.bisect_right(it, t) - 1
    return (ind[i][1], ind[i][2], i) if i >= 0 else (None, None, -1)

print("share of H1 bars by ADX, per year")
yr = defaultdict(lambda: [0, 0, 0])
for (t, *_), (_, _, a) in zip(h1, ind):
    if a is None: continue
    y = dt.datetime.fromtimestamp(t, dt.timezone.utc).year
    yr[y][0] += 1; yr[y][1] += a < 20; yr[y][2] += a < 25
for y in sorted(yr):
    n_, a20, a25 = yr[y]
    print(f"  {y}: ADX<20 {100*a20/n_:4.0f}%   ADX<25 {100*a25/n_:4.0f}%")

def rng(src, t, hidx):
    if src in ("h12", "h24", "h48"):
        k = int(src[1:])
        if hidx - k + 1 < 0: return None
        bars = h1[hidx - k + 1: hidx + 1]
        return max(b[2] for b in bars), min(b[3] for b in bars)
    day = dt.datetime.fromtimestamp(t, dt.timezone.utc).date()
    prev = [b for b in h1[max(0, hidx - 60): hidx + 1] if dt.datetime.fromtimestamp(b[0], dt.timezone.utc).date() < day]
    if not prev: return None
    last = dt.datetime.fromtimestamp(prev[-1][0], dt.timezone.utc).date()
    pd = [b for b in prev if dt.datetime.fromtimestamp(b[0], dt.timezone.utc).date() == last]
    return max(b[2] for b in pd), min(b[3] for b in pd)

res = defaultdict(lambda: [0, 0, 0])
for src in ("h12", "h24", "h48", "pday"):
    last_ev = {}
    for j, (t, hi, lo, c) in enumerate(m5):
        a, x, hidx = state(t)
        if a is None or x is None: continue
        r = rng(src, t, hidx)
        if not r: continue
        H, L = r
        w = H - L
        if w < a: continue
        side = "S" if hi > H and L < c < H else ("B" if lo < L and L < c < H else None)
        if not side or t - last_ev.get(side, 0) < 3600: continue
        last_ev[side] = t
        mid = (H + L) / 2
        stop = hi + 0.5 * a if side == "S" else lo - 0.5 * a
        out = None
        for t2, h2, l2, _ in m5[j + 1: j + 1 + 288]:
            if side == "S":
                if h2 >= stop: out = "break"; break
                if l2 <= mid: out = "mid"; break
            else:
                if l2 <= stop: out = "break"; break
                if h2 >= mid: out = "mid"; break
        bucket = "ADX<20" if x < 20 else ("20-25" if x < 25 else "ADX>=25")
        k = (src, bucket)
        res[k][0] += 1
        res[k][1] += out == "mid"
        res[k][2] += out == "break"

print("\nfalse break back inside: which comes first, range middle or 0.5 ATR beyond the poke?")
print(f"  {'range':6}{'ADX':>9}{'events':>8}{'mid first':>11}{'break first':>13}")
for k in sorted(res):
    n_, m, b = res[k]
    print(f"  {k[0]:6}{k[1]:>9}{n_:8}{100*m/n_:10.0f}%{100*b/n_:12.0f}%")

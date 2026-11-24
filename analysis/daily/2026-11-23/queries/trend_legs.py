"""Counts clean trend legs in gold: zigzag on M5 highs/lows that reverses only after a pullback of 1x H1 ATR(14);
a leg of 3x+ ATR is a clean trend (it never gave back more than 1 ATR on the way). Optional: trend_follow backtest
trades split by whether they opened inside such a leg (hindsight labelling, to see where the profit comes from).
Usage: python3 -I trend_legs.py <gold-history dir> [trend_follow_trades.csv]
"""
import bisect
import csv
import datetime as dt
import statistics as st
import sys
from collections import Counter, defaultdict

EAT = dt.timezone(dt.timedelta(hours=3))
REV, MIN_LEG, BIG_LEG = 1.0, 3.0, 5.0
d = sys.argv[1]
h1 = [(int(r[0]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
atr_t, atr_v, a, pc = [], [], None, None
for t, hi, lo, c in h1:
    tr = hi - lo if pc is None else max(hi - lo, abs(hi - pc), abs(lo - pc))
    a = tr if a is None else (a * 13 + tr) / 14
    pc = c
    atr_t.append(t + 3600)
    atr_v.append(a)
m5 = [(int(r[0]), float(r[2]), float(r[3])) for r in csv.reader(open(f"{d}/M5.csv"))]


def atr_at(t):
    i = bisect.bisect_right(atr_t, t) - 1
    return atr_v[max(i, 0)]


legs = []
direc, ext, ext_t, start, start_t, start_atr = 0, None, None, None, None, None
for t, hi, lo in m5:
    if direc == 0:
        direc, ext, ext_t, start, start_t, start_atr = 1, hi, t, lo, t, atr_at(t)
        continue
    thr = REV * atr_at(ext_t)
    if direc > 0:
        if hi > ext:
            ext, ext_t = hi, t
        elif ext - lo >= thr:
            legs.append((start_t, ext_t, direc, ext - start, start_atr))
            direc, start, start_t, start_atr, ext, ext_t = -1, ext, ext_t, atr_at(ext_t), lo, t
    else:
        if lo < ext:
            ext, ext_t = lo, t
        elif hi - ext >= thr:
            legs.append((start_t, ext_t, direc, start - ext, start_atr))
            direc, start, start_t, start_atr, ext, ext_t = 1, ext, ext_t, atr_at(ext_t), hi, t

clean = [l for l in legs if l[3] >= MIN_LEG * l[4]]
big = [l for l in clean if l[3] >= BIG_LEG * l[4]]
days = defaultdict(set)
for t, _, _ in m5:
    days[dt.datetime.fromtimestamp(t, EAT).year].add(dt.datetime.fromtimestamp(t, EAT).date())
weeks = defaultdict(set)
for t, _, _ in m5:
    x = dt.datetime.fromtimestamp(t, EAT)
    weeks[x.year].add(x.isocalendar()[:2])
yr = lambda l: dt.datetime.fromtimestamp(l[0], EAT).year
print(f"{len(legs)} zigzag legs (reversal = {REV} H1 ATR); clean trend = {MIN_LEG}+ ATR, big = {BIG_LEG}+ ATR\n")
print(f"{'year':6}{'days':>6}{'clean':>7}{'/day':>6}{'/week':>7}{'big':>5}{'/week':>7}{'up/down':>9}{'avg $':>8}{'avg ATR':>8}{'avg hrs':>8}{'% time':>8}")
for y in sorted(days):
    c = [l for l in clean if yr(l) == y]
    b = [l for l in big if yr(l) == y]
    secs = sum(l[1] - l[0] for l in c)
    tot = len(days[y]) * 23 * 3600
    print(f"{y:<6}{len(days[y]):6}{len(c):7}{len(c)/len(days[y]):6.2f}{len(c)/len(weeks[y]):7.1f}{len(b):5}{len(b)/len(weeks[y]):7.1f}"
          f"{sum(l[2]>0 for l in c):>5}/{sum(l[2]<0 for l in c):<3}{st.mean(l[3] for l in c):8.1f}{st.mean(l[3]/l[4] for l in c):8.1f}"
          f"{st.mean((l[1]-l[0])/3600 for l in c):8.1f}{100*secs/tot:7.0f}%")
wk = Counter()
for l in clean:
    x = dt.datetime.fromtimestamp(l[0], EAT)
    wk[x.isocalendar()[:2]] += 1
allw = set().union(*weeks.values())
dist = Counter(min(wk.get(w, 0), 6) for w in allw)
print("\nweeks by number of clean legs (6 = 6+):", dict(sorted(dist.items())), f" median {st.median(wk.get(w,0) for w in allw)}")
dd = Counter()
for l in clean:
    dd[dt.datetime.fromtimestamp(l[0], EAT).date()] += 1
alld = set().union(*days.values())
print("days by number of clean legs starting (3 = 3+):", dict(sorted(Counter(min(dd.get(x, 0), 3) for x in alld).items())))
print("\nclean legs by start hour EAT:", " ".join(f"{h:02d}:{n}" for h, n in sorted(Counter(dt.datetime.fromtimestamp(l[0], EAT).hour for l in clean).items())))
print("clean legs by weekday:", dict(sorted(Counter(dt.datetime.fromtimestamp(l[0], EAT).strftime('%a') for l in clean).items(), key=lambda kv: ['Mon','Tue','Wed','Thu','Fri','Sat','Sun'].index(kv[0]))))

if len(sys.argv) > 2:
    tr = [r for r in csv.reader(open(sys.argv[2])) if r[0] in ("BUY", "SELL")]
    iv = sorted((l[0], l[1], l[2]) for l in clean)
    starts = [x[0] for x in iv]
    buckets = defaultdict(list)
    for r in tr:
        o, side, R = int(r[1]), r[0], float(r[4])
        i = bisect.bisect_right(starts, o) - 1
        k = "outside a clean leg"
        if i >= 0 and iv[i][0] <= o <= iv[i][1]:
            k = "inside, with the leg" if (iv[i][2] > 0) == (side == "BUY") else "inside, against the leg"
        buckets[k].append(R)
    print("\ntrend_follow 6-year backtest trades by where they opened:")
    for k, v in sorted(buckets.items()):
        print(f"  {k:26} n={len(v):5}  total {sum(v):7.1f}R  avg {sum(v)/len(v):+.3f}R  win {100*sum(x>0 for x in v)/len(v):3.0f}%")

print("\nknowable in real time: once a move has gone X ATR without a 1-ATR pullback, how far does it end up?")
sizes = [l[3] / l[4] for l in legs]
for x in (1.0, 1.5, 2.0, 2.5, 3.0):
    s = [v for v in sizes if v >= x]
    print(f"  reached {x:.1f} ATR: {len(s):6} legs | P(ends >= 3 ATR) {100*sum(v>=3 for v in s)/len(s):3.0f}%  P(>= 5) {100*sum(v>=5 for v in s)/len(s):3.0f}%"
          f"  median further run {st.median(v-x for v in s):.2f} ATR (then gives back 1 ATR)")

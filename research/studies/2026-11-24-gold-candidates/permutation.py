"""Permutation controls for the two candidates (independent of the backtester).
Month start: session (D1, re-open to close) returns on the first 3 weekdays of each month vs 2,000 random picks of
3 weekday sessions per month. Evening dip: next-bar-to-+3h returns after a strong down H1 candle opening 11-12 New
York vs 2,000 random picks of the same number of H1 bars at the same hours (no candle condition).
Usage: python3 -I permutation.py <gold dir> <oil dir>
"""
import csv, datetime as dt, random, statistics as st, sys
from zoneinfo import ZoneInfo
NY = ZoneInfo("America/New_York")
random.seed(11)

def sessions(d):
    out = {}
    for r in csv.reader(open(f"{d}/D1.csv")):
        t, o, c = int(r[0]), float(r[1]), float(r[4])
        sd = (dt.datetime.fromtimestamp(t, dt.timezone.utc).astimezone(NY) + dt.timedelta(hours=7)).date()
        if sd.weekday() < 5:
            out[sd] = (c - o) / o * 1e4
    return out

def wdom(d):
    return sum(1 for k in range(1, d.day + 1) if dt.date(d.year, d.month, k).weekday() < 5)

def month_start(name, d):
    s = sessions(d)
    months = {}
    for day, bp in s.items():
        months.setdefault((day.year, day.month), []).append((day, bp))
    actual = {}
    for k, v in months.items():
        actual[k] = sum(bp for day, bp in v if wdom(day) <= 3)
    tot = sum(actual.values())
    perm = []
    for _ in range(2000):
        x = 0.0
        for v in months.values():
            pick = random.sample(v, min(3, len(v)))
            x += sum(bp for _, bp in pick)
        perm.append(x)
    pct = 100 * sum(p < tot for p in perm) / len(perm)
    early = sum(v for (y, _), v in actual.items() if y <= 2020); late = sum(v for (y, _), v in actual.items() if y >= 2021)
    print(f"{name} month start (first 3 weekdays): total {tot:+.0f} bp; random 3-day picks median {st.median(perm):+.0f} bp, "
          f"5-95% [{sorted(perm)[100]:+.0f}, {sorted(perm)[1900]:+.0f}]; actual beats {pct:.1f}% of random   (2016-20 {early:+.0f}, 2021-26 {late:+.0f})")

def evening_dip(d):
    B = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
    pos = {b[0]: i for i, b in enumerate(B)}
    cand, dips = [], []
    for i in range(len(B) - 4):
        t, o, h, l, c = B[i]
        ny = dt.datetime.fromtimestamp(t, dt.timezone.utc).astimezone(NY)
        if ny.weekday() >= 5 or ny.hour not in (11, 12): continue
        if B[i + 3][0] - B[i + 1][0] != 7200: continue  # contiguous, no break inside
        r = (B[i + 3][4] - B[i + 1][1]) / B[i + 1][1] * 1e4
        cand.append(r)
        if h > l and c < o and o - c > 0.6 * (h - l):
            dips.append((ny.year, r))
    act = st.mean(r for _, r in dips)
    perm = [st.mean(random.sample(cand, len(dips))) for _ in range(2000)]
    pct = 100 * sum(p < act for p in perm) / len(perm)
    yrs = {}
    for y, r in dips: yrs.setdefault(y, []).append(r)
    print(f"gold evening dip (strong down H1 opening 11-12 NY, hold ~3h): {len(dips)} events, mean {act:+.2f} bp; "
          f"same hours without the dip: mean {st.mean(cand):+.2f} bp; random same-size picks 95th pct {sorted(perm)[1900]:+.2f}; "
          f"actual beats {pct:.1f}% of random")
    print("   per year mean bp: " + " ".join(f"{y%100}:{st.mean(v):+.1f}" for y, v in sorted(yrs.items())))

month_start("gold", sys.argv[1])
month_start("oil ", sys.argv[2])
evening_dip(sys.argv[1])

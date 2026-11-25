"""Can gold's exhaustion be seen at the time? 2016-2026.
Event: at an H1 close, the last 24h moved >= 3 H1 ATR one way and the close is within 0.3 ATR of that extreme.
Outcome (M5 path, up to 24h): reversal = 1.5 ATR back first; continuation = 1 ATR further first.
Each feature is measured with data up to the event only. Reported per feature value: events, P(reversal first),
and the same for 2016-2021 and 2022-2026.
Usage: python3 -I exhaustion.py <download dir> <out.csv>
"""
import bisect, csv, datetime as dt, statistics as st, sys

d = sys.argv[1]
H = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4]), float(r[5])) for r in csv.reader(open(f"{d}/H1.csv"))]
M5 = [(int(r[0]), float(r[2]), float(r[3])) for r in csv.reader(open(f"{d}/M5.csv"))]
M5t = [m[0] for m in M5]
N = len(H)
T = [h[0] for h in H]; O = [h[1] for h in H]; HI = [h[2] for h in H]; LO = [h[3] for h in H]; C = [h[4] for h in H]; V = [h[5] for h in H]

atr = [0.0] * N; rsi = [50.0] * N
a = g = l = None
for i in range(1, N):
    tr = max(HI[i] - LO[i], abs(HI[i] - C[i - 1]), abs(LO[i] - C[i - 1])); ch = C[i] - C[i - 1]
    if a is None: a, g, l = tr, max(ch, 0), max(-ch, 0)
    else: a += (tr - a) / 14; g += (max(ch, 0) - g) / 14; l += (max(-ch, 0) - l) / 14
    atr[i] = a; rsi[i] = 100 - 100 / (1 + g / l) if l else 100

# M5 RSI at each M5 close
m5c = [float(r[4]) for r in csv.reader(open(f"{d}/M5.csv"))]
m5rsi = [50.0] * len(m5c); g = l = None
for i in range(1, len(m5c)):
    ch = m5c[i] - m5c[i - 1]
    if g is None: g, l = max(ch, 0), max(-ch, 0)
    else: g += (max(ch, 0) - g) / 14; l += (max(-ch, 0) - l) / 14
    m5rsi[i] = 100 - 100 / (1 + g / l) if l else 100

day_rng = {}       # full-day range, used only for PAST days (ADR)
day_so_far = [None] * N  # range of the current UTC day up to and including bar i (no look-ahead)
for i in range(N):
    k = dt.datetime.fromtimestamp(T[i], dt.timezone.utc).date()
    h_, l_ = day_rng.get(k, (HI[i], LO[i])); day_rng[k] = (max(h_, HI[i]), min(l_, LO[i]))
    day_so_far[i] = day_rng[k]
days = sorted(day_rng)
adr = {}
for j, k in enumerate(days):
    prev = [day_rng[x][0] - day_rng[x][1] for x in days[max(0, j - 20):j]]
    adr[k] = st.mean(prev) if prev else None

events = []
last = -100
for i in range(300, N - 30):
    A = atr[i]
    if A <= 0 or i - last < 6: continue
    w = range(i - 23, i + 1)
    hi24, lo24 = max(HI[k] for k in w), min(LO[k] for k in w)
    up = C[i] - lo24 >= 3 * A and hi24 - C[i] <= 0.3 * A
    dn = hi24 - C[i] >= 3 * A and C[i] - lo24 <= 0.3 * A
    if not (up or dn): continue
    s = 1 if up else -1
    last = i
    # outcome on M5 after the H1 close
    t0 = T[i] + 3600; j = bisect.bisect_left(M5t, t0); px = C[i]; out = None
    for t, h5, l5 in M5[j:j + 288]:
        if s > 0:
            if l5 <= px - 1.5 * A: out = "rev"; break
            if h5 >= px + 1.0 * A: out = "cont"; break
        else:
            if h5 >= px + 1.5 * A: out = "rev"; break
            if l5 <= px - 1.0 * A: out = "cont"; break
    if out is None: continue
    # features
    ext_i = max(w, key=lambda k: HI[k]) if s > 0 else min(w, key=lambda k: LO[k])
    prev_w = range(i - 47, i - 11)  # previous swing extreme 12-48h ago
    pk = max(prev_w, key=lambda k: HI[k]) if s > 0 else min(prev_w, key=lambda k: LO[k])
    new_ext = HI[i] > HI[pk] if s > 0 else LO[i] < LO[pk]
    divergence = new_ext and (rsi[i] < rsi[pk] - 3 if s > 0 else rsi[i] > rsi[pk] + 3)
    vavg = st.mean(V[i - 20:i]) or 1
    rg = HI[i] - LO[i]
    wick = ((HI[i] - C[i]) if s > 0 else (C[i] - LO[i])) / rg if rg > 0 else 0
    leg_start = min(w, key=lambda k: LO[k]) if s > 0 else max(w, key=lambda k: HI[k])
    speed_h = i - leg_start + 1
    run = 0
    for k in range(i, i - 24, -1):
        if (C[k] > O[k]) == (s > 0): run += 1
        else: break
    u = dt.datetime.fromtimestamp(T[i] + 3600, dt.timezone.utc)
    day = dt.datetime.fromtimestamp(T[i], dt.timezone.utc).date(); a20 = adr.get(day)
    dr = day_so_far[i]; dayx = (dr[0] - dr[1]) / a20 if a20 and dr else 0
    r_dir = rsi[i] if s > 0 else 100 - rsi[i]
    jm = bisect.bisect_right(M5t, T[i] + 3600 - 300) - 1
    m5r = m5rsi[jm] if s > 0 else 100 - m5rsi[jm]
    events.append(dict(
        year=u.year, out=out, side="up" if s > 0 else "down",
        divergence="yes" if divergence else "no",
        volume=">=2x" if V[i] >= 2 * vavg else ("1.3-2x" if V[i] >= 1.3 * vavg else "<1.3x"),
        wick=">=50%" if wick >= 0.5 else ("25-50%" if wick >= 0.25 else "<25%"),
        day_range=">=2x ADR" if dayx >= 2 else ("1.5-2x" if dayx >= 1.5 else "<1.5x"),
        speed="<=6h" if speed_h <= 6 else ("7-14h" if speed_h <= 14 else ">14h"),
        h1_rsi=">=80" if r_dir >= 80 else ("70-80" if r_dir >= 70 else "<70"),
        m5_rsi=">=85" if m5r >= 85 else ("70-85" if m5r >= 70 else "<70"),
        run=">=5" if run >= 5 else ("3-4" if run >= 3 else "<3"),
        hour_utc=f"{(u.hour // 4) * 4:02d}-{(u.hour // 4) * 4 + 4:02d}"))

base = sum(e["out"] == "rev" for e in events) / len(events)
ins = [e for e in events if e["year"] < 2022]; oos = [e for e in events if e["year"] >= 2022]
p = lambda v: 100 * sum(e["out"] == "rev" for e in v) / len(v) if v else float("nan")
print(f"{len(events)} stretched-move events; reversal (1.5 ATR back) came first in {100*base:.0f}%, "
      f"continuation (1 ATR further) in {100-100*base:.0f}%   [2016-21: {p(ins):.0f}%, 2022-26: {p(oos):.0f}%]")
print("break-even for a counter-trend trade risking 1 ATR to make 1.5 ATR is 40% reversal-first.\n")
rows = []
for f in ("side", "divergence", "volume", "wick", "day_range", "speed", "h1_rsi", "m5_rsi", "run", "hour_utc"):
    print(f"{f}:")
    for val in sorted({e[f] for e in events}):
        v = [e for e in events if e[f] == val]
        a_, b_ = [e for e in v if e["year"] < 2022], [e for e in v if e["year"] >= 2022]
        yrs = {}
        for e in v: yrs.setdefault(e["year"], []).append(e["out"] == "rev")
        above = sum(sum(x) / len(x) > base for x in yrs.values())
        print(f"   {val:10} n={len(v):5}  reversal first {p(v):3.0f}%   (16-21 {p(a_):3.0f}%, 22-26 {p(b_):3.0f}%)   years above base {above}/{len(yrs)}")
        rows.append((f, val, len(v), p(v), p(a_), p(b_), above))
with open(sys.argv[2], "w", newline="") as fh:
    w = csv.writer(fh); w.writerow(["feature", "value", "n", "rev_pct", "rev_16_21", "rev_22_26", "years_above_base"]); w.writerows(rows)

# combinations: count of exhaustion signs present
def score(e):
    return sum([e["divergence"] == "yes", e["volume"] == ">=2x", e["wick"] == ">=50%", e["day_range"] != "<1.5x",
                e["h1_rsi"] == ">=80", e["m5_rsi"] == ">=85", e["run"] == ">=5"])
print("\nnumber of exhaustion signs present (divergence, volume >=2x, wick >=50%, day range >=1.5x ADR, H1 RSI >=80, M5 RSI >=85, run >=5):")
for k in range(0, 8):
    v = [e for e in events if score(e) == k]
    if len(v) >= 20:
        print(f"   {k} signs  n={len(v):5}  reversal first {p(v):3.0f}%   (16-21 {p([e for e in v if e['year']<2022]):3.0f}%, 22-26 {p([e for e in v if e['year']>=2022]):3.0f}%)")

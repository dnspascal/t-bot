"""Gold after scheduled US releases, detected from price (no calendar): at 08:30, 10:00 and 14:00 New York time, the
first 5 minutes are a shock when their move is >= Z times the median 5-minute move of the previous 2 hours.
Then: enter at T+5m (as the bot can, on the M5 close) in the shock direction and hold 1/2/4h. Reports continuation
(same direction) per event, net of cost per 1 oz. Usage: python3 -I news_shocks.py <dir with M1.csv>
"""
import bisect, csv, datetime as dt, statistics as st, sys
from zoneinfo import ZoneInfo
NY = ZoneInfo("America/New_York")
M = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{sys.argv[1]}/M1.csv"))]
T = [m[0] for m in M]

def price_at(t):  # open of the first M1 bar at or after t
    i = bisect.bisect_left(T, t)
    return (M[i][1], i) if i < len(M) and M[i][0] - t < 300 else (None, None)

days = sorted({dt.datetime.fromtimestamp(t, dt.timezone.utc).astimezone(NY).date() for t in T[::1440]})
days = sorted({d for d in days} | {dt.datetime.fromtimestamp(t, dt.timezone.utc).astimezone(NY).date() for t in T[::300]})
res = []
for d in days:
    if d.weekday() >= 5: continue
    for hh, mm in ((8, 30), (10, 0), (14, 0)):
        t0 = int(dt.datetime(d.year, d.month, d.day, hh, mm, tzinfo=NY).timestamp())
        p0, i0 = price_at(t0); p5, i5 = price_at(t0 + 300)
        if p0 is None or p5 is None: continue
        base = []
        for k in range(24):
            a, _ = price_at(t0 - 7200 + k * 300); b, _ = price_at(t0 - 7200 + (k + 1) * 300)
            if a is not None and b is not None: base.append(abs(b - a))
        if len(base) < 18: continue
        med = st.median(base)
        if med <= 0: continue
        z = abs(p5 - p0) / med
        s = 1 if p5 > p0 else -1
        fwd = {}
        for h in (1, 2, 4):
            ph, _ = price_at(t0 + 300 + h * 3600)
            fwd[h] = None if ph is None else s * (ph - p5)
        res.append((d.year, f"{hh:02d}:{mm:02d}", z, p5 - p0, fwd, p0))

for Z in (3, 5, 8):
    print(f"\n== shocks with first-5-min move >= {Z}x normal: trade IN the shock direction from T+5m (net of $0.20, 1 oz)")
    print(f"  {'time NY':8}{'events':>7}" + "".join(f"{'hold '+str(h)+'h $':>14}{'same dir':>9}" for h in (1, 2, 4)))
    for when in ("08:30", "10:00", "14:00", "all"):
        ev = [r for r in res if r[2] >= Z and (when == "all" or r[1] == when)]
        if len(ev) < 10: continue
        line = f"  {when:8}{len(ev):7}"
        for h in (1, 2, 4):
            v = [r[4][h] for r in ev if r[4][h] is not None]
            line += f"{sum(x - 0.20 for x in v):14.0f}{100*sum(x > 0 for x in v)/len(v):8.0f}%"
        print(line)

ev = [r for r in res if r[2] >= 5]
print("\n== shocks >= 5x, all times, per year: hold 2h in shock direction, $ net of $0.20")
for y in range(2016, 2027):
    v = [r[4][2] for r in ev if r[0] == y and r[4][2] is not None]
    if v: print(f"  {y}: {len(v):3} events  ${sum(x - 0.20 for x in v):+7.0f}  same direction {100*sum(x>0 for x in v)/len(v):3.0f}%")

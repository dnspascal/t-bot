"""Gold holding-period study, 2016-2026, H1 bars. Signals at an H1 close, entry at the next H1 open, exit after a fixed
number of trading hours (or at a stop). One position at a time per config. Costs per 1 oz: cost per trade (spread +
commission) plus swap — buys pay 0.019% of price per night (Wednesday x3, from prod fills), sells get no credit
(conservative: the credit only exists while USD rates are high).
Usage: python3 -I holding.py <download dir with H1.csv> <out.csv>
"""
import csv, datetime as dt, math, statistics as st, sys

d, out = sys.argv[1], sys.argv[2]
B = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{d}/H1.csv"))]
T = [b[0] for b in B]; O = [b[1] for b in B]; H = [b[2] for b in B]; L = [b[3] for b in B]; C = [b[4] for b in B]
N = len(B)
YEAR = [dt.datetime.fromtimestamp(t, dt.timezone.utc).year for t in T]
HOUR = [dt.datetime.fromtimestamp(t, dt.timezone.utc).hour for t in T]
WD = [dt.datetime.fromtimestamp(t, dt.timezone.utc).weekday() for t in T]
LONG_SWAP = 0.80 / 4200  # fraction of price per night

def ema(x, n):
    a, e, outv = 2 / (n + 1), None, []
    for v in x:
        e = v if e is None else e + a * (v - e)
        outv.append(e)
    return outv

# day range proxy: mean of the last 14 rolling 23-bar ranges, sampled every 23 bars
rng23 = [max(H[max(0, i - 22):i + 1]) - min(L[max(0, i - 22):i + 1]) for i in range(N)]
ADR = [st.mean(rng23[max(0, i - 13 * 23):i + 1:23]) for i in range(N)]
WARM = 4700

def nights(t0, t1):
    """Swap charges between t0 and t1 (rollover 21:00 UTC Mon-Fri, Wednesday counts 3)."""
    n = 0
    day = dt.datetime.fromtimestamp(t0, dt.timezone.utc).replace(hour=21, minute=0, second=0)
    if day.timestamp() <= t0:
        day += dt.timedelta(days=1)
    while day.timestamp() <= t1:
        if day.weekday() < 5:
            n += 3 if day.weekday() == 2 else 1
        day += dt.timedelta(days=1)
    return n

def simulate(sig, hold, stop_mult, cost):
    """sig[i] in {+1,-1,0}: decided at bar i close, entry at bar i+1 open."""
    trades = []
    i = WARM
    while i < N - 2:
        s = sig[i]
        if not s:
            i += 1
            continue
        e = i + 1
        x = min(e + hold, N - 1)
        entry = O[e]
        stop = entry - s * stop_mult * ADR[i] if stop_mult else None
        exitp, xi = O[x], x
        if stop is not None:
            for k in range(e, x):
                if s > 0 and L[k] <= stop or s < 0 and H[k] >= stop:
                    exitp, xi = stop, k
                    break
        gross = s * (exitp - entry)
        swap = -LONG_SWAP * entry * nights(T[e], T[xi]) if s > 0 else 0.0
        trades.append((YEAR[e], s, gross - cost + swap, gross))
        i = xi
    return trades

def summarize(name, fam, hold, stop, trades):
    by = {y: 0.0 for y in range(2016, 2027)}
    lo = sh = 0.0
    eq = peak = dd = 0.0
    for y, s, net, _ in trades:
        by[y] += net
        if s > 0: lo += net
        else: sh += net
        eq += net; peak = max(peak, eq); dd = min(dd, eq - peak)
    tot = sum(by.values())
    return dict(name=name, fam=fam, hold=hold, stop=stop, n=len(trades), tot=tot, long=lo, short=sh,
                ins=sum(by[y] for y in range(2016, 2021)), oos=sum(by[y] for y in range(2021, 2027)),
                ypos=sum(v > 0 for v in by.values()), dd=dd, by=by,
                gross=sum(t[3] for t in trades), per=tot / len(trades) if trades else 0)

HOLDS = [4, 8, 12, 24, 48, 72, 120, 240, 460]
LOOKS = {"12h": 12, "1d": 23, "3d": 69, "5d": 115, "10d": 230, "20d": 460, "60d": 1380}
signals = {}
for k, n in LOOKS.items():
    mom = [0] * N
    for i in range(n, N):
        mom[i] = 1 if C[i] > C[i - n] else (-1 if C[i] < C[i - n] else 0)
    signals[("mom", k)] = mom
    signals[("fade", k)] = [-v for v in mom]
for k, n in {"50h": 50, "100h": 100, "200h": 200, "20d": 460, "50d": 1150, "100d": 2300, "200d": 4600}.items():
    e = ema(C, n)
    signals[("ema", k)] = [1 if C[i] > e[i] else -1 for i in range(N)]
for k, n in {"1d": 23, "2d": 46, "5d": 115, "20d": 460, "55d": 1265}.items():
    s = [0] * N
    for i in range(n, N):
        hi, lo = max(H[i - n:i]), min(L[i - n:i])
        s[i] = 1 if C[i] > hi else (-1 if C[i] < lo else 0)
    signals[("donch", k)] = s

results = []
for cost in (0.20, 0.40):
    for (fam, k), sig in signals.items():
        for hold in HOLDS:
            for stop in (0, 1.0):
                r = summarize(f"{fam}_{k}_h{hold}_s{stop:.0f}", fam, hold, stop, simulate(sig, hold, stop, cost))
                r["cost"] = cost
                results.append(r)
    for hr in range(24):
        for side in (1, -1):
            sig = [side if HOUR[i] == (hr - 1) % 24 and WD[i] < 5 else 0 for i in range(N)]
            for hold in (4, 8, 12):
                r = summarize(f"hour{hr:02d}_{'buy' if side > 0 else 'sell'}_h{hold}", "hour", hold, 0, simulate(sig, hold, 0, cost))
                r["cost"] = cost
                results.append(r)
    print(f"cost {cost}: {len(results)} results so far", file=sys.stderr)

with open(out, "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["name", "fam", "hold", "stop", "cost", "n", "total", "long", "short", "is_16_20", "oos_21_26", "yrs_pos", "max_dd", "per_trade"] + [str(y) for y in range(2016, 2027)])
    for r in results:
        w.writerow([r["name"], r["fam"], r["hold"], r["stop"], r["cost"], r["n"], f"{r['tot']:.1f}", f"{r['long']:.1f}", f"{r['short']:.1f}",
                    f"{r['ins']:.1f}", f"{r['oos']:.1f}", r["ypos"], f"{r['dd']:.1f}", f"{r['per']:.2f}"] + [f"{r['by'][y]:.1f}" for y in range(2016, 2027)])

# buy-and-hold reference (with swap), per year
print("\nbuy and hold 1 oz each year (price change minus swap):")
for y in range(2016, 2027):
    idx = [i for i in range(N) if YEAR[i] == y]
    a, b = idx[0], idx[-1]
    print(f"  {y}: {C[b] - O[a]:+8.1f}  swap {-LONG_SWAP * O[a] * nights(T[a], T[b]):+7.1f}")

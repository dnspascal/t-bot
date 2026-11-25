"""Does the dollar (EURUSD) or silver (XAGUSD) tell us where gold goes next? 2016-2026, only past data at each step,
gold is the only thing traded. Costs: $0.20/oz per trade, buys pay swap 0.019% of price per night.
Null: the other market shifted by whole weeks (the link is broken, everything else is kept).
Usage: python3 -I crossasset.py <raw/ctrader dir>
"""
import csv, math, statistics as st, sys
from collections import deque

class Roll:
    """running mean/variance (and covariance with a second series) over the last n values"""
    def __init__(self, n):
        self.n, self.q = n, deque()
        self.sx = self.sxx = self.sy = self.syy = self.sxy = 0.0
    def add(self, x, y=0.0):
        self.q.append((x, y)); self.sx += x; self.sxx += x * x; self.sy += y; self.syy += y * y; self.sxy += x * y
        if len(self.q) > self.n:
            a, b = self.q.popleft(); self.sx -= a; self.sxx -= a * a; self.sy -= b; self.syy -= b * b; self.sxy -= a * b
    def __len__(self): return len(self.q)
    def sd(self):
        k = len(self.q); m = self.sx / k
        return math.sqrt(max(self.sxx / k - m * m, 0))
    def beta(self):  # slope of y on x
        k = len(self.q); mx, my = self.sx / k, self.sy / k
        v = self.sxx / k - mx * mx
        return (self.sxy / k - mx * my) / v if v > 0 else 0.0

R = sys.argv[1]
def load(sym, tf):
    return {int(r[0]): float(r[4]) for r in csv.reader(open(f"{R}/{sym}/2026-11-24_from2016/{tf}.csv"))}
SWAP = 0.80 / 4200
import datetime as dt
def year(t): return dt.datetime.fromtimestamp(t, dt.timezone.utc).year

def corr(a, b):
    ma, mb = st.mean(a), st.mean(b)
    num = sum((x - ma) * (y - mb) for x, y in zip(a, b))
    return num / math.sqrt(sum((x - ma) ** 2 for x in a) * sum((y - mb) ** 2 for y in b))

# ---------- 1. M5 co-movement and lead-lag ----------
G5, E5, S5 = load("XAUUSD", "M5"), load("EURUSD", "M5"), load("XAGUSD", "M5")
ts = sorted(t for t in G5 if t in E5 and t in S5)
rows = []  # (t, g_ret, e_ret, s_ret) for contiguous 5-minute steps
for a, b in zip(ts, ts[1:]):
    if b - a == 300:
        rows.append((b, math.log(G5[b] / G5[a]), math.log(E5[b] / E5[a]), math.log(S5[b] / S5[a])))
g = [r[1] for r in rows]; e = [r[2] for r in rows]; s = [r[3] for r in rows]
print(f"== 1. 5-minute returns, {len(rows)} aligned steps")
print(f"   same 5 minutes: corr(gold, EURUSD) {corr(g, e):+.3f}   corr(gold, silver) {corr(g, s):+.3f}")
for k in (1, 2, 3, 6):
    print(f"   other market now vs gold {5*k:>2} min later: EURUSD {corr(g[k:], e[:-k]):+.4f}   silver {corr(g[k:], s[:-k]):+.4f}   gold itself {corr(g[k:], g[:-k]):+.4f}")

# ---------- 2. dollar / silver shock, gold has not followed ----------
def shocks(other, label, shift=0):
    o = other if not shift else other[shift:] + other[:shift]
    sd = Roll(288)
    res = {15: [], 60: [], 240: []}
    T = [r[0] for r in rows]
    gp = [G5[t] for t in T]
    last = -10 ** 9
    for i in range(len(rows) - 50):
        x = o[i]
        if len(sd) == 288:
            z = x / (sd.sd() or 1e-12)
            if abs(z) >= 4 and i - last >= 48:
                implied = 1 if x > 0 else -1
                if label == "EURUSD":
                    implied = implied  # EURUSD up = dollar down = gold up
                if g[i] * implied < abs(g[i]) * 0.25 or abs(g[i]) < abs(x) * 0.5:  # gold lagged
                    last = i
                    for hm, steps in ((15, 3), (60, 12), (240, 48)):
                        if T[i + steps] - T[i] == steps * 300:
                            pnl = implied * (gp[i + steps] - gp[i]) - 0.20 - (SWAP * gp[i] * steps / 288 if implied > 0 else 0)
                            res[hm].append((year(T[i]), pnl))
        sd.add(x)
    return res

def report(name, res):
    out = []
    for hm, v in res.items():
        if not v:
            continue
        ins = [p for y, p in v if y < 2022]; oos = [p for y, p in v if y >= 2022]
        yrs = {}
        for y, p in v: yrs[y] = yrs.get(y, 0) + p
        out.append(f"hold {hm:>3}m n={len(v):5} ${sum(ins):+6.0f} / ${sum(oos):+6.0f} (16-21 / 22-26) yrs+ {sum(x>0 for x in yrs.values()):2}/11")
    print(f"   {name}:  " + " | ".join(out))

print("\n== 2. shock in the other market (>= 4 sigma in 5 min), gold had not followed: trade gold the implied way")
for lab, arr in (("EURUSD", e), ("silver", s)):
    report(f"{lab:6} real", shocks(arr, lab))
    report(f"{lab:6} null", shocks(arr, lab, shift=2016 * 7))

# ---------- 3 & 4. H1 residual: gold vs what the dollar / silver imply ----------
G1, E1, S1 = load("XAUUSD", "H1"), load("EURUSD", "H1"), load("XAGUSD", "H1")
def residual_test(other, label, shift_bars=0):
    T = sorted(t for t in G1 if t in other)
    if shift_bars:
        vals = [other[t] for t in T]; vals = vals[shift_bars:] + vals[:shift_bars]
        other = dict(zip(T, vals))
    lg = [math.log(G1[t]) for t in T]; lo = [math.log(other[t]) for t in T]
    W = 480
    reg = Roll(W); resid = {4: Roll(W), 24: Roll(W)}
    out = {}
    lastt = {}
    for i in range(25, len(T) - 121):
        if T[i] - T[i - 1] == 3600:
            reg.add(lo[i] - lo[i - 1], lg[i] - lg[i - 1])
        if len(reg) < W:
            continue
        beta = reg.beta()
        for H in (4, 24):
            r = (lg[i] - lg[i - H]) - beta * (lo[i] - lo[i - H])
            q = resid[H]
            if len(q) >= 100:
                z = r / (q.sd() or 1e-12)
                if abs(z) >= 2:
                    side = -1 if z > 0 else 1  # gold ran ahead of what the other market implies: fade it
                    for hold in (4, 24, 120):
                        key = (H, hold)
                        if T[i] - lastt.get(key, -10 ** 12) < hold * 3600 or T[i + hold] - T[i] > hold * 3600 * 1.6:
                            continue
                        lastt[key] = T[i]
                        px0, px1 = G1[T[i]], G1[T[i + hold]]
                        nights = round((T[i + hold] - T[i]) / 86400)
                        pnl = side * (px1 - px0) - 0.20 - (SWAP * px0 * nights if side > 0 else 0)
                        out.setdefault(key, []).append((year(T[i]), pnl))
            q.add(r)
        if i % 24 == 0:
            pass
    return out

print("\n== 3/4. gold moved >= 2 sigma more/less than the other market implies over H hours: bet gold corrects (H1, beta from the past 20 days)")
for lab, other in (("EURUSD", E1), ("silver", S1)):
    for tag, sh in (("real", 0), ("null", 24 * 5 * 26)):
        res = residual_test(other, lab, sh)
        for (H, hold), v in sorted(res.items()):
            ins = [p for y, p in v if y < 2022]; oos = [p for y, p in v if y >= 2022]
            yrs = {}
            for y, p in v: yrs[y] = yrs.get(y, 0) + p
            print(f"   {lab:6} {tag}  last {H:>2}h, hold {hold:>3}h: n={len(v):5}  ${sum(ins):+7.0f} / ${sum(oos):+7.0f} (16-21 / 22-26)  yrs+ {sum(x>0 for x in yrs.values()):2}/11")

"""Systematic pattern search on gold H1 bars, 2016-2026, with out-of-sample validation and a luck baseline.
Every single feature bin and every pair of feature bins is a condition; for each condition and holding time (1h, 4h,
24h) the direction is chosen on 2016-2021 and judged on 2022-2026. Trades never overlap (one position at a time).
Costs per trade: $0.20 per oz plus swap on buys (0.019% of price per night, expected share for short holds).
Null: the same search with forward returns circularly shifted by whole weeks (features no longer line up with
what happened next), repeated SHIFTS times.
Usage: python3 -I search.py <dir with H1.csv>
"""
import csv, datetime as dt, itertools, math, random, statistics as st, sys
from collections import defaultdict

B = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(f"{sys.argv[1]}/H1.csv"))]
N = len(B)
T = [b[0] for b in B]; O = [b[1] for b in B]; H = [b[2] for b in B]; L = [b[3] for b in B]; C = [b[4] for b in B]
U = [dt.datetime.fromtimestamp(t, dt.timezone.utc) for t in T]
YEAR = [u.year for u in U]
HOLDS = (1, 4, 24)
SWAP = 0.80 / 4200
SPLIT = 2022
import os
T_MIN = float(os.environ.get("T_MIN", "3"))
YRS_MIN = int(os.environ.get("YRS_MIN", "5"))

def ema(x, n):
    a, e, out = 2 / (n + 1), None, []
    for v in x:
        e = v if e is None else e + a * (v - e); out.append(e)
    return out

atr = [0.0] * N; rsi = [50.0] * N; adx = [0.0] * N
a_ = g_ = l_ = p_ = m_ = x_ = None
for i in range(1, N):
    tr = max(H[i] - L[i], abs(H[i] - C[i - 1]), abs(L[i] - C[i - 1]))
    ch = C[i] - C[i - 1]
    up, dn = H[i] - H[i - 1], L[i - 1] - L[i]
    pdm, mdm = (up if up > dn and up > 0 else 0), (dn if dn > up and dn > 0 else 0)
    if a_ is None:
        a_, g_, l_, p_, m_ = tr, max(ch, 0), max(-ch, 0), pdm, mdm
    else:
        a_ += (tr - a_) / 14; g_ += (max(ch, 0) - g_) / 14; l_ += (max(-ch, 0) - l_) / 14
        p_ += (pdm - p_) / 14; m_ += (mdm - m_) / 14
    atr[i] = a_
    rsi[i] = 100 - 100 / (1 + g_ / l_) if l_ else 100
    pdi, mdi = 100 * p_ / a_, 100 * m_ / a_
    dx = 100 * abs(pdi - mdi) / (pdi + mdi) if pdi + mdi else 0
    x_ = dx if x_ is None else x_ + (dx - x_) / 14
    adx[i] = x_
e50, e200 = ema(C, 50), ema(C, 200)

def binv(v, cuts, names):
    for c, n in zip(cuts, names):
        if v < c:
            return n
    return names[-1]

# previous UTC day's high/low and trading day of month
dayhl, prevhl = {}, {}
for i in range(N):
    d = U[i].date(); h, l = dayhl.get(d, (H[i], L[i])); dayhl[d] = (max(h, H[i]), min(l, L[i]))
days = sorted(dayhl)
for k in range(1, len(days)):
    prevhl[days[k]] = dayhl[days[k - 1]]
tdom = {}
for d in days:
    tdom.setdefault((d.year, d.month), []).append(d)
dom = {}
for (y, m), ds in tdom.items():
    ds = [d for d in ds if d.weekday() < 5]
    for k, d in enumerate(ds):
        dom[d] = "first3" if k < 3 else ("last" if k == len(ds) - 1 else "mid")

START = 600
FEAT = []
for i in range(N):
    if i < START:
        FEAT.append(None); continue
    u, a = U[i], atr[i]
    hr = u.hour
    f = {
        "sess": "asia" if hr >= 22 or hr < 7 else ("london" if hr < 12 else ("ny" if hr < 17 else "late")),
        "dow": "MTWRF"[u.weekday()] if u.weekday() < 5 else "S",
        "rsi": binv(rsi[i], (30, 45, 55, 70), ("<30", "30-45", "45-55", "55-70", ">70")),
        "adx": binv(adx[i], (20, 30), ("<20", "20-30", ">30")),
        "trend": "e50>e200" if e50[i] > e200[i] else "e50<e200",
        "dist50": binv((C[i] - e50[i]) / a, (-2, -0.5, 0.5, 2), ("<-2", "-2..-.5", "near", ".5..2", ">2")),
        "r4h": binv((C[i] - C[i - 4]) / a, (-1.5, -0.5, 0.5, 1.5), ("<-1.5", "-1.5..-.5", "flat", ".5..1.5", ">1.5")),
        "r24h": binv((C[i] - C[i - 24]) / (a * 4.8), (-1, -0.3, 0.3, 1), ("<-1", "-1..-.3", "flat", ".3..1", ">1")),
        "r5d": binv((C[i] - C[i - 115]) / (a * 10.7), (-0.5, 0.5), ("down", "flat", "up")),
        "vol": binv(sum(atr[k] < a for k in range(i - 500, i, 5)) / 100, (0.3, 0.7), ("low", "mid", "high")),
        "rng24": binv((C[i] - min(L[i - 23:i + 1])) / max(1e-9, max(H[i - 23:i + 1]) - min(L[i - 23:i + 1])), (0.2, 0.8), ("bottom", "middle", "top")),
        "pday": "na",
        "candle": "other",
        "month": dom.get(u.date(), "mid"),
    }
    ph = prevhl.get(u.date())
    if ph:
        f["pday"] = "above" if C[i] > ph[0] else ("below" if C[i] < ph[1] else "inside")
    rg = H[i] - L[i]
    if rg > 0 and abs(C[i] - O[i]) > 0.6 * rg:
        f["candle"] = "strong_up" if C[i] > O[i] else "strong_down"
    FEAT.append(f)
KEYS = list(FEAT[START].keys())

def forward(shift):
    """net-of-nothing forward returns (entry next open, exit open after h bars), in $ per oz; shifted for the null."""
    out = {}
    for h in HOLDS:
        r = [None] * N
        for i in range(START, N - h - 1):
            j = i + shift if shift else i
            j = START + (j - START) % (N - h - 1 - START)
            r[i] = (O[j + 1 + h] - O[j + 1], O[j + 1])
        out[h] = r
    return out

def cost(h, side, px):
    c = 0.20
    if side > 0:
        c += SWAP * px * (1 if h >= 24 else h / 24)
    return c

def run(shift):
    FW = forward(shift)
    cells = defaultdict(list)
    for i in range(START, N - 26):
        f = FEAT[i]
        conds = [(k, f[k]) for k in KEYS]
        for c in conds:
            cells[(c,)].append(i)
        for c1, c2 in itertools.combinations(conds, 2):
            cells[(c1, c2)].append(i)
    found = []
    for cond, idx in cells.items():
        if len(idx) < 300:
            continue
        for h in HOLDS:
            fw = FW[h]
            ins, oos = [], []
            last = -10 ** 9
            for i in idx:
                if i - last < h or fw[i] is None:
                    continue
                last = i
                (ins if YEAR[i] < SPLIT else oos).append((YEAR[i], fw[i]))
            if len(ins) < 150 or len(oos) < 100:
                continue
            m = st.mean(x[1][0] for x in ins)
            side = 1 if m > 0 else -1
            net_in = [side * x[1][0] - cost(h, side, x[1][1]) for x in ins]
            if st.mean(net_in) <= 0:
                continue
            sd = st.pstdev(net_in) or 1
            t_in = st.mean(net_in) / (sd / math.sqrt(len(net_in)))
            yrs_in = defaultdict(float)
            for (y, _), v in zip(ins, net_in):
                yrs_in[y] += v
            if t_in < T_MIN or sum(v > 0 for v in yrs_in.values()) < YRS_MIN:
                continue
            net_oos = [side * x[1][0] - cost(h, side, x[1][1]) for x in oos]
            yrs_oos = defaultdict(float)
            for (y, _), v in zip(oos, net_oos):
                yrs_oos[y] += v
            found.append(dict(cond=cond, h=h, side=side, n_in=len(ins), n_oos=len(oos), t_in=t_in,
                              in_usd=sum(net_in), oos_usd=sum(net_oos), oos_yrs=sum(v > 0 for v in yrs_oos.values()),
                              oos_per=st.mean(net_oos)))
    passed = [r for r in found if r["oos_usd"] > 0 and r["oos_yrs"] >= 4]
    return found, passed

random.seed(7)
real_found, real_pass = run(0)
print(f"REAL: {len(real_found)} conditions pass the 2016-2021 screen (t >= {T_MIN}, {YRS_MIN}+ of 6 years); "
      f"{len(real_pass)} also profitable 2022-2026 in 4+ of 5 years")
nulls = []
for k in range(3):
    s = random.randint(20, 200) * 115
    nf, np_ = run(s)
    nulls.append((len(nf), len(np_)))
    print(f"NULL shift {s} h: {len(nf)} pass the screen, {len(np_)} also pass out-of-sample")

def name(r):
    return " & ".join(f"{k}={v}" for k, v in r["cond"])
print(f"\nREAL survivors, best out-of-sample first ({'buy' if True else ''} side shown):")
print(f"  {'condition':58}{'hold':>5}{'side':>5}{'n 16-21':>8}{'t':>6}{'$ 16-21':>9}{'n 22-26':>8}{'$ 22-26':>9}{'yrs+':>5}{'$/trade':>8}")
for r in sorted(real_pass, key=lambda r: -r["oos_usd"])[:30]:
    print(f"  {name(r):58}{r['h']:5}{'buy' if r['side']>0 else 'sell':>5}{r['n_in']:8}{r['t_in']:6.1f}{r['in_usd']:9.0f}{r['n_oos']:8}{r['oos_usd']:9.0f}{r['oos_yrs']:5}{r['oos_per']:8.2f}")
with open(sys.argv[2] if len(sys.argv) > 2 else "/dev/null", "w") as f:
    w = csv.writer(f)
    w.writerow(["condition", "hold", "side", "n_in", "t_in", "usd_in", "n_oos", "usd_oos", "oos_yrs", "oos_per_trade"])
    for r in sorted(real_found, key=lambda r: -r["oos_usd"]):
        w.writerow([name(r), r["h"], r["side"], r["n_in"], f"{r['t_in']:.2f}", f"{r['in_usd']:.1f}", r["n_oos"], f"{r['oos_usd']:.1f}", r["oos_yrs"], f"{r['oos_per']:.2f}"])

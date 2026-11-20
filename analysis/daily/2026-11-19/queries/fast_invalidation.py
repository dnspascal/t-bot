"""Ways to make invalidation react faster without trading on 1-minute noise, replayed on live XAUUSD trades with the
live exit stack (staged profit lock 50->10, 75->60, 90->70; stall; never-profitable; EOD; no peak drawback).
Reuses invalidation_speed.py for the M5 and every-minute M5 signal tables. Market exits fill at the next M1 open.
Usage: python3 -I fast_invalidation.py <positions.tsv> <m1.tsv> <m5_full.tsv> <m1_states.tsv> <m1_open.tsv>
"""
import bisect
import contextlib
import datetime as dt
import io
import runpy
import sys
from collections import Counter, defaultdict

with contextlib.redirect_stdout(io.StringIO()):
    S = runpy.run_path("invalidation_speed.py")
pos, m1, m1t, sig, against, eod, nxt = S["pos"], S["m1"], S["m1t"], S["sig"], S["against"], S["eod"], S["nxt"]
NO_WATCHER = S["NO_WATCHER"]
M5, MIN = sig["m5 close (live)"], sig["m5 every minute"]
STEPS = ((0.5, 0.1), (0.75, 0.6), (0.9, 0.7))


def sim(p, rule):
    s = 1 if p["side"] == "BUY" else -1
    op, sl, tp = float(p["open_price"]), float(p["sl"]), float(p["tp"])
    g = lambda x: s * (x - op)
    tpd, sl0, risk = g(tp), g(sl), -g(sl)
    stop, peak, streak = sl0, 0.0, 0
    ot = int(p["ot"])
    watch = p["strat"] not in NO_WATCHER
    i = bisect.bisect_left(m1t, ot - ot % 60)
    for t, h, l in m1[i:i + 60 * 24 * 7]:
        fill = lambda: g(nxt.get(t + 60, (h + l) / 2))
        if eod(t):
            return "eod", fill()
        adv, fav = (l, h) if s > 0 else (h, l)
        if g(adv) <= stop:
            return ("SL" if stop == sl0 else "lock"), stop
        if g(fav) >= tpd:
            return "TP", tpd
        peak = max(peak, g(fav))
        for a, lk in STEPS:
            if peak >= a * tpd:
                stop = max(stop, max(lk * tpd, 0.5))
        age = t + 60 - ot
        if 1800 <= age < 1860 and peak <= 0.1:
            return "never", fill()
        if 3600 <= age < 3660 and peak < 0.15 * tpd:
            return "stall", fill()
        if not watch or t + 60 <= ot + 60:
            continue
        close = g(nxt.get(t + 60, (h + l) / 2))
        st5, stm = M5.get(t + 60), MIN.get(t + 60)
        n5 = against(st5, s) if st5 else None
        nm = against(stm, s) if stm else 0
        streak = streak + 1 if nm >= 3 else 0
        if rule(n5, nm, streak, close, risk, peak, tpd):
            return "inval", close
    return "open", 0.0


R = {
    "live: M5 close 3/4": lambda n5, nm, k, c, r, pk, tpd: n5 is not None and n5 >= 3,
    "every minute 3/4": lambda n5, nm, k, c, r, pk, tpd: nm >= 3,
    "every minute 3/4 held 2 min": lambda n5, nm, k, c, r, pk, tpd: k >= 2,
    "every minute 3/4 held 3 min": lambda n5, nm, k, c, r, pk, tpd: k >= 3,
    "live OR (every-min 3/4 & loss>=30% risk)": lambda n5, nm, k, c, r, pk, tpd: (n5 is not None and n5 >= 3) or (nm >= 3 and c <= -0.3 * r),
    "live OR (every-min 2/4 & loss>=50% risk)": lambda n5, nm, k, c, r, pk, tpd: (n5 is not None and n5 >= 3) or (nm >= 2 and c <= -0.5 * r),
    "live OR (every-min 3/4 & was +20% TP, now <0)": lambda n5, nm, k, c, r, pk, tpd: (n5 is not None and n5 >= 3) or (nm >= 3 and pk >= 0.2 * tpd and c < 0),
    "live OR (every-min 2/4 & was +30% TP, now <-20% risk)": lambda n5, nm, k, c, r, pk, tpd: (n5 is not None and n5 >= 3) or (nm >= 2 and pk >= 0.3 * tpd and c <= -0.2 * r),
}
mon = lambda p: dt.datetime.fromtimestamp(int(p["ot"]), dt.timezone.utc).strftime("%m")
months = sorted({mon(p) for p in pos})
w = [i for i, p in enumerate(pos) if p["strat"] not in NO_WATCHER]
res = {k: [sim(p, f) for p in pos] for k, f in R.items()}
base = res["live: M5 close 3/4"]
print(f"{len(pos)} trades, {len(w)} use invalidation; $/oz\n{'invalidation rule':52}{'total':>8}{'vs live':>9}{'inval':>6}{'TP':>5}{'SL':>5}  months>=live  " + " ".join(f"{m:>6}" for m in months))
for k, rs in res.items():
    by, bb = defaultdict(float), defaultdict(float)
    for p, r, b in zip(pos, rs, base):
        by[mon(p)] += r[1]
        bb[mon(p)] += b[1]
    c = Counter(r[0] for r in rs)
    tot = sum(r[1] for r in rs)
    print(f"{k:52}{tot:8.1f}{tot-sum(b[1] for b in base):9.1f}{c['inval']:6}{c['TP']:5}{c['SL']:5}  {sum(1 for m in months if by[m] >= bb[m]-0.01)}/{len(months)}          " + " ".join(f"{by[m]:6.1f}" for m in months))
for k in list(res)[1:]:
    ch = [(b, x) for b, x in zip(base, res[k]) if abs(b[1] - x[1]) > 0.01]
    up = [x[1] - b[1] for b, x in ch if x[1] > b[1]]
    dn = [x[1] - b[1] for b, x in ch if x[1] < b[1]]
    print(f"  {k:52} changed {len(ch):3}: helped {len(up):3} (+{sum(up):7.1f})  hurt {len(dn):3} ({sum(dn):7.1f})")
t0 = int(dt.datetime(2026, 11, 19, 21, 0, tzinfo=dt.timezone.utc).timestamp())
print("\ntoday:")
for i, p in enumerate(pos):
    if int(p["ot"]) >= t0:
        print(f"  {dt.datetime.fromtimestamp(int(p['ot']), dt.timezone(dt.timedelta(hours=3))).strftime('%H:%M')} real {float(p['real']):7.2f} | " +
              "  ".join(f"{res[k][i][0]}:{res[k][i][1]:.1f}" for k in res))

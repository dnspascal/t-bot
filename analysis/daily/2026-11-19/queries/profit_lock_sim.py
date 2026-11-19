"""Live XAUUSD trades (Jul 2 - now) replayed on M1 with the current exit stack (invalidation on M5 close, stall,
never-profitable, EOD) and different ways of moving the SL once a trade is in profit.
Usage: python3 -I profit_lock_sim.py <positions.tsv> <m1.tsv> <m5_states.tsv>
"""
import bisect
import csv
import datetime as dt
import sys
from collections import defaultdict

NO_WATCHER = {"ema_pullback", "sr_bounce", "session_momentum", "dd_oversold_bounce", "rsi_reversal"}
pos = list(csv.DictReader(open(sys.argv[1]), delimiter="\t"))
m1 = [(int(r[0]), float(r[1]), float(r[2])) for r in list(csv.reader(open(sys.argv[2]), delimiter="\t"))[1:]]
m1t = [b[0] for b in m1]
m5 = {int(r[0]): (float(r[1]), r[2], float(r[3]), float(r[4]), float(r[5]), r[6])
      for r in list(csv.reader(open(sys.argv[3]), delimiter="\t"))[1:]}


def eod(t):
    d = dt.datetime.fromtimestamp(t, dt.timezone.utc)
    return d.weekday() < 5 and d.hour == 21 and d.minute >= 30


def steps(*pairs):
    return lambda peak, tpd: max([lk * tpd for at, lk in pairs if peak >= at * tpd], default=-1e9)


def trail(arm, keep):
    return lambda peak, tpd: peak * keep if peak >= arm * tpd else -1e9


def sim(p, lock, pd=False):
    s = 1 if p["side"] == "BUY" else -1
    op, sl, tp = float(p["open_price"]), float(p["sl"]), float(p["tp"])
    g = lambda x: s * (x - op)
    tpd, sl0, peak = g(tp), g(sl), 0.0
    stop = sl0
    ot = int(p["ot"])
    watch = p["strat"] not in NO_WATCHER
    i = bisect.bisect_left(m1t, ot - ot % 60)
    for t, h, l in m1[i:i + 60 * 24 * 7]:
        if eod(t):
            return "eod", g((h + l) / 2)
        adv, fav = (l, h) if s > 0 else (h, l)
        if g(adv) <= stop:
            return ("SL" if stop == sl0 else "lock"), stop
        if g(fav) >= tpd:
            return "TP", tpd
        if pd and peak >= tpd / 2 and g(adv) <= peak * 0.25:
            return "pd", peak * 0.25
        peak = max(peak, g(fav))
        stop = max(stop, lock(peak, tpd))
        if pd and peak >= tpd / 2 and g(adv) <= peak * 0.25:
            return "pd", peak * 0.25
        age = t + 60 - ot
        if 1800 <= age < 1860 and peak <= 0.1:
            return "never", g((h + l) / 2)
        if 3600 <= age < 3660 and peak < 0.15 * tpd:
            return "stall", g((h + l) / 2)
        st = m5.get(t + 60)
        if watch and st:
            c, reg, rsi, ef, es, mom = st
            n = (reg in (("trending_down", "ranging") if s > 0 else ("trending_up", "ranging"))) + \
                ((rsi < 50) if s > 0 else (rsi > 50)) + ((ef < es) if s > 0 else (ef > es)) + \
                (mom == ("falling" if s > 0 else "rising"))
            if n >= 3:
                return "inval", g(c)
    return "open", 0.0


be = lambda peak, tpd: 0.5 if peak >= tpd / 2 else -1e9
variants = {
    "old: PD on + BE $0.50": (be, True),
    "now: BE $0.50 only": (be, False),
    "at 50% TP lock 25%": (steps((0.5, 0.25)), False),
    "at 50% lock 25%, 75% lock 50%": (steps((0.5, 0.25), (0.75, 0.5)), False),
    "40%->15%, 60%->35%, 80%->55%": (steps((0.4, 0.15), (0.6, 0.35), (0.8, 0.55)), False),
    "trail: keep 50% of peak (arm 50%)": (trail(0.5, 0.5), False),
    "trail: keep 60% of peak (arm 50%)": (trail(0.5, 0.6), False),
    "trail: keep 50% of peak (arm 30%)": (trail(0.3, 0.5), False),
}
mon = lambda p: dt.datetime.fromtimestamp(int(p["ot"]), dt.timezone.utc).strftime("%m")
months = sorted({mon(p) for p in pos})
res = {k: [sim(p, *v) for p in pos] for k, v in variants.items()}
now = res["now: BE $0.50 only"]
print(f"{len(pos)} trades, $/oz\n{'exit rule':36}{'total':>9}{'vs now':>8}  " + " ".join(f"{m:>7}" for m in months) + "   months beating now")
for k, rs in res.items():
    bym, bymn = defaultdict(float), defaultdict(float)
    for p, r, n in zip(pos, rs, now):
        bym[mon(p)] += r[1]
        bymn[mon(p)] += n[1]
    tot = sum(r[1] for r in rs)
    beat = sum(1 for m in months if bym[m] > bymn[m] + 0.01)
    print(f"{k:36}{tot:9.1f}{tot-sum(r[1] for r in now):8.1f}  " + " ".join(f"{bym[m]:7.1f}" for m in months) + f"   {beat}/{len(months)}")

print("\ntoday's trades (Nov 19 from 10:00 EAT):")
today = [i for i, p in enumerate(pos) if int(p["ot"]) >= int(dt.datetime(2026, 11, 19, 7, tzinfo=dt.timezone.utc).timestamp())]
for i in today:
    p = pos[i]
    print(f"  {p['strat']:17}{p['side']:5}{float(p['open_price']):9.2f} real {float(p['real']):7.2f} | " +
          "  ".join(f"{k.split(':')[0][:14]}={res[k][i][1]:.2f}" for k in list(res)[2:]))

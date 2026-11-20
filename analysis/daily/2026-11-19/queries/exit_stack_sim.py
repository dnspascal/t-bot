"""Replays every live XAUUSD trade (Jul 2 - now) through the bot's current exit stack on M1 bars + recorded M5 states,
then switches single protections off / swaps peak drawback for a fixed SL profit lock, to see who catches what.
Usage: python3 -I exit_stack_sim.py <positions.tsv> <m1.tsv> <m5_states.tsv>
"""
import bisect
import csv
import datetime as dt
import sys
from collections import Counter, defaultdict

BUF = 0.5
NO_WATCHER = {"ema_pullback", "sr_bounce", "session_momentum", "dd_oversold_bounce", "rsi_reversal"}

pos = list(csv.DictReader(open(sys.argv[1]), delimiter="\t"))
m1 = [(int(r[0]), float(r[1]), float(r[2])) for r in list(csv.reader(open(sys.argv[2]), delimiter="\t"))[1:]]
m1t = [b[0] for b in m1]
m5 = {}
for r in list(csv.reader(open(sys.argv[3]), delimiter="\t"))[1:]:
    m5[int(r[0])] = (float(r[1]), r[2], float(r[3]), float(r[4]), float(r[5]), r[6])


def eod(t):
    d = dt.datetime.fromtimestamp(t, dt.timezone.utc)
    return d.weekday() < 5 and d.hour == 21 and d.minute >= 30


def sim(p, pd=True, inval=True, stall=True, never=True, be=True, lock=None):
    s = 1 if p["side"] == "BUY" else -1
    op, sl, tp = float(p["open_price"]), float(p["sl"]), float(p["tp"])
    g = lambda x: s * (x - op)
    tpd, stop, peak = g(tp), g(sl), 0.0
    arm = tpd / 2
    ot = int(p["ot"])
    watch = inval and p["strat"] not in NO_WATCHER
    i = bisect.bisect_left(m1t, ot - ot % 60)
    for t, h, l in m1[i:i + 60 * 24 * 7]:
        if eod(t):
            return "eod", g((h + l) / 2), t
        adv, fav = (l, h) if s > 0 else (h, l)
        if g(adv) <= stop:
            kind = "SL" if stop == g(sl) else ("lock" if stop > BUF else "breakeven")
            return kind, stop, t
        if g(fav) >= tpd:
            return "TP", tpd, t
        if pd and peak >= arm and g(adv) <= peak * 0.25:
            return "peak drawback", peak * 0.25, t
        peak = max(peak, g(fav))
        if peak >= arm:
            if lock is not None:
                stop = max(stop, lock * tpd)
            elif be:
                stop = max(stop, BUF)
        if pd and peak >= arm and g(adv) <= peak * 0.25:
            return "peak drawback", peak * 0.25, t
        age = t + 60 - ot
        if never and 1800 <= age < 1860 and peak <= 0.1:
            return "never-profitable", g((h + l) / 2), t
        if stall and 3600 <= age < 3660 and peak < 0.15 * tpd:
            return "stall", g((h + l) / 2), t
        st = m5.get(t + 60)
        if watch and st and (t + 60) % 300 == 0:
            c, reg, rsi, ef, es, mom = st
            n = 0
            n += reg in (("trending_down", "ranging") if s > 0 else ("trending_up", "ranging"))
            n += (rsi < 50) if s > 0 else (rsi > 50)
            n += (ef < es) if s > 0 else (ef > es)
            n += mom == ("falling" if s > 0 else "rising")
            if n >= 3:
                return "invalidation", g(c), t
    return "open", 0.0, 0


variants = {
    "current stack": {},
    "no peak drawback": dict(pd=False),
    "no PD, lock 25% TP": dict(pd=False, lock=0.25),
    "no PD, lock 35% TP": dict(pd=False, lock=0.35),
    "no PD, lock 45% TP": dict(pd=False, lock=0.45),
    "no invalidation": dict(inval=False),
    "no stall stop": dict(stall=False),
    "no never-profitable": dict(never=False),
}
res = {k: [sim(p, **v) for p in pos] for k, v in variants.items()}
real = [float(p["real"]) for p in pos]

base = res["current stack"]
print(f"trades {len(pos)}  real total ${sum(real):.2f}/oz  sim current-stack ${sum(r[1] for r in base):.2f}/oz")
print("current-stack exit mix:", dict(Counter(r[0] for r in base)))
print(f"\n{'variant':22}{'total $/oz':>11}{'vs current':>12}   by month")
months = sorted({dt.datetime.fromtimestamp(int(p['ot']), dt.timezone.utc).strftime('%m') for p in pos})
for k, rs in res.items():
    bym = defaultdict(float)
    for p, r in zip(pos, rs):
        bym[dt.datetime.fromtimestamp(int(p["ot"]), dt.timezone.utc).strftime("%m")] += r[1]
    tot = sum(r[1] for r in rs)
    print(f"{k:22}{tot:11.2f}{tot-sum(r[1] for r in base):12.2f}   " + " ".join(f"{m}:{bym[m]:7.1f}" for m in months))

for off, name in (("no peak drawback", "peak drawback"), ("no invalidation", "invalidation"), ("no stall stop", "stall")):
    print(f"\ntrades the current stack closes by {name} — with {name} OFF, who closes them instead:")
    c = defaultdict(lambda: [0, 0.0, 0.0])
    for b, a in zip(base, res[off]):
        if b[0] == name:
            c[a[0]][0] += 1
            c[a[0]][1] += b[1]
            c[a[0]][2] += a[1]
    for k, (n, kept, new) in sorted(c.items(), key=lambda kv: -kv[1][0]):
        print(f"  {k:18}{n:4}  with {name}: {kept:8.2f}   without: {new:8.2f}   diff {new-kept:8.2f}")
    if name == "peak drawback":
        inv = [(b, a) for b, a in zip(base, res[off]) if b[0] == name and a[0] == "invalidation"]
        if inv:
            print(f"    of those caught by invalidation instead: {sum(1 for _, a in inv if a[1] < 0)} closed in loss, avg {sum(a[1] for _, a in inv)/len(inv):.2f}")

print("\nby strategy, $/oz:")
strats = sorted({p["strat"] for p in pos})
print(f"{'strategy':20}{'n':>4}" + "".join(f"{k[:18]:>20}" for k in res))
for st in strats:
    idx = [i for i, p in enumerate(pos) if p["strat"] == st]
    print(f"{st:20}{len(idx):4}" + "".join(f"{sum(res[k][i][1] for i in idx):20.2f}" for k in res))

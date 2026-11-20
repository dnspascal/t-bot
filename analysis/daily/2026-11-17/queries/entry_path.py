"""Do the bot's entries beat random entries in the first minutes and hours?
For every live XAUUSD trade since July, replays the M1 path after entry and races its real SL vs TP,
then does the same for 20 random entries per trade (same side, same SL/TP distances, same UTC hour, other days).
Usage: python3 -I entry_path.py <m1.csv> <trades.tsv>
"""
import bisect, csv, random, sys
from collections import defaultdict

random.seed(7)
m1 = [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4])) for r in csv.reader(open(sys.argv[1]))]
ts = [b[0] for b in m1]
by_hour = defaultdict(list)
for i, b in enumerate(m1):
    by_hour[(b[0] // 3600) % 24].append(i)
CHECK = [1, 5, 15, 30, 60, 120]
HORIZON = 360


def walk(i0, side, entry, sl_d, tp_d):
    """Race SL vs TP from bar i0; returns (outcome, best gain before outcome, gain path at CHECK minutes)."""
    best, out, path = 0.0, "none", {}
    sgn = 1 if side == "BUY" else -1
    for k in range(HORIZON):
        i = i0 + k
        if i >= len(m1) or m1[i][0] - m1[i0][0] > (HORIZON + 120) * 60:
            break
        _, o, h, l, c = m1[i]
        fav, adv = (h - entry, entry - l) if sgn > 0 else (entry - l, h - entry)
        if out == "none":
            if adv >= sl_d:
                out = "sl"
            elif fav >= tp_d:
                out = "tp"
            else:
                best = max(best, fav)
        if k + 1 in CHECK:
            path[k + 1] = sgn * (c - entry)
    return out, best, path


trades = []
for r in csv.reader(open(sys.argv[2]), delimiter="\t"):
    t, side, entry, sl, tp = int(r[0]), r[3], float(r[4]), float(r[5]), float(r[6])
    trades.append(dict(t=t, strat=r[2], side=side, entry=entry, sl_d=abs(sl - entry), tp_d=abs(tp - entry), week=(t + 3 * 86400) // (7 * 86400)))


def summary(rows):
    n = len(rows)
    sl = [x for x in rows if x[0] == "sl"]
    return dict(n=n, sl=100 * len(sl) / n, tp=100 * sum(x[0] == "tp" for x in rows) / n,
                sl_after5=100 * sum(x[1] >= 5 for x in sl) / n,
                path={k: sum(x[2].get(k, 0) for x in rows) / n for k in CHECK})


real, rand = defaultdict(list), defaultdict(list)
for tr in trades:
    i0 = bisect.bisect_left(ts, tr["t"] // 60 * 60)
    if i0 >= len(m1):
        continue
    res = walk(i0, tr["side"], tr["entry"], tr["sl_d"], tr["tp_d"])
    for key in ("ALL", tr["strat"]):
        real[key].append(res)
    pool = by_hour[(tr["t"] // 3600) % 24]
    for _ in range(20):
        j = random.choice(pool)
        if abs(m1[j][0] - tr["t"]) < 86400:
            continue
        rr = walk(j, tr["side"], m1[j][1], tr["sl_d"], tr["tp_d"])
        for key in ("ALL", tr["strat"]):
            rand[key].append(rr)

print(f"{'group':20}{'':6}{'n':>6}{'SL%':>6}{'TP%':>6}{'+$5 then SL%':>14}  avg $ after " + " ".join(f"{k:>6}m" for k in CHECK))
for key in ["ALL"] + sorted(k for k in real if k != "ALL"):
    if len(real[key]) < 15:
        continue
    for name, rows in (("bot", real[key]), ("random", rand[key])):
        s = summary(rows)
        print(f"{key if name == 'bot' else '':20}{name:6}{s['n']:6}{s['sl']:6.0f}{s['tp']:6.0f}{s['sl_after5']:14.0f}  {'':12}" + " ".join(f"{s['path'][k]:7.2f}" for k in CHECK))

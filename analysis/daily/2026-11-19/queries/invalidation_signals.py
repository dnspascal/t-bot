"""For live XAUUSD trades the bot closed by invalidation: when did each of the four M5 reversal signals turn against
the trade, measured from the trade's best price, and which one was the last to complete the 3-of-4.
Usage: python3 -I invalidation_signals.py <positions.tsv> <m1.tsv> <m5_full.tsv>
"""
import bisect
import csv
import statistics as st
import sys
from collections import Counter

pos = [p for p in csv.DictReader(open(sys.argv[1]), delimiter="\t") if "against" in p["reason"]]
m1 = [(int(r[0]), float(r[1]), float(r[2])) for r in list(csv.reader(open(sys.argv[2]), delimiter="\t"))[1:]]
m1t = [b[0] for b in m1]
m5 = [(int(r[0]), float(r[3]), r[4], float(r[5]), float(r[6]), float(r[7]), r[8]) for r in list(csv.reader(open(sys.argv[3]), delimiter="\t"))[1:]]
m5t = [b[0] for b in m5]
NAMES = ("regime", "rsi", "ema_cross", "momentum")


def sigs(b, s):
    _, c, reg, rsi, ef, es, mom = b
    return (reg in (("trending_down", "ranging") if s > 0 else ("trending_up", "ranging")),
            (rsi < 50) if s > 0 else (rsi > 50),
            (ef < es) if s > 0 else (ef > es),
            mom == ("falling" if s > 0 else "rising"))


lag, lost, last, ever, at_entry = {n: [] for n in NAMES}, {n: [] for n in NAMES}, Counter(), Counter(), Counter()
peak_to_exit, giveback = [], []
for p in pos:
    s = 1 if p["side"] == "BUY" else -1
    op, sl = float(p["open_price"]), float(p["sl"])
    risk = abs(op - sl)
    ot, xt = int(p["ot"]), int(p["xt"])
    i, j = bisect.bisect_left(m1t, ot - ot % 60), bisect.bisect_right(m1t, xt)
    if j <= i:
        continue
    best_t, best = ot, 0.0
    for t, h, l in m1[i:j]:
        g = s * ((h if s > 0 else l) - op)
        if g > best:
            best, best_t = g, t
    k0, k1 = bisect.bisect_right(m5t, ot), bisect.bisect_right(m5t, xt)
    bars = m5[k0:k1]
    if not bars:
        continue
    for n, v in zip(NAMES, sigs(m5[k0 - 1], s)):
        at_entry[n] += v
    exit_g = s * (bars[-1][1] - op)
    peak_to_exit.append((bars[-1][0] - best_t) / 60)
    giveback.append((best - exit_g) / risk)
    final = sigs(bars[-1], s)
    turned = {}
    for n_idx, n in enumerate(NAMES):
        if not final[n_idx]:
            continue
        ever[n] += 1
        k = len(bars) - 1
        while k > 0 and sigs(bars[k - 1], s)[n_idx]:
            k -= 1
        turned[n] = bars[k][0]
        lag[n].append((bars[k][0] - best_t) / 60)
        lost[n].append((best - s * (bars[k][1] - op)) / risk)
    if turned:
        last[max(turned, key=turned.get)] += 1

print(f"{len(pos)} live trades closed by invalidation")
print(f"median: best price -> exit {st.median(peak_to_exit):.0f} min, gave back {st.median(giveback):.2f} x risk from the best price\n")
print(f"{'signal':11}{'against at entry':>18}{'against at exit':>17}{'turned, min after best':>24}{'giveback when turned':>22}{'last to turn':>14}")
for n in NAMES:
    print(f"{n:11}{at_entry[n]:>11} ({at_entry[n]/len(pos):.0%}){ever[n]:>10} ({ever[n]/len(pos):.0%})"
          f"{st.median(lag[n]) if lag[n] else 0:>17.0f} med{st.median(lost[n]) if lost[n] else 0:>15.2f} x risk{last[n]:>11}")

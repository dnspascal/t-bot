"""For every live XAUUSD close by a protection (peak drawback, breakeven, invalidation, time stop, EOD), replay what
the original SL/TP would have done from the exit minute on M1 bars. Saved = protection result better than untouched.
Usage: python3 -I protection_audit.py <closes.tsv> <m1.tsv>
"""
import bisect
import csv
import sys
from collections import defaultdict

HORIZON = 72 * 3600

bars = [(int(r[0]), float(r[1]), float(r[2])) for r in list(csv.reader(open(sys.argv[2]), delimiter="\t"))[1:]]
times = [b[0] for b in bars]


def mech(reason):
    if reason.startswith("peak_drawback"):
        return "peak drawback"
    if reason == "breakeven_sl":
        return "breakeven SL"
    if "against" in reason:
        return "invalidation"
    if reason.startswith("time_stop"):
        return "time stop (" + reason.split("=")[1].split("_")[0] + ")"
    return reason


rows = defaultdict(list)
detail = []
for r in csv.DictReader(open(sys.argv[1]), delimiter="\t"):
    op, sl, tp = float(r["open_price"]), float(r["sl"]), float(r["tp"])
    if op < 100:
        continue
    reason = r["reason"]
    if reason in ("sl_hit", "tp_hit"):
        continue
    s = 1 if r["side"] == "BUY" else -1
    g = lambda p: s * (p - op)
    oz = float(r["oz"])
    real = float(r["gross_profit"]) / oz
    xt = int(r["xt"])
    i = bisect.bisect_right(times, xt - xt % 60)
    out, cf, peak, at = "open", None, real, None
    for t, h, l in bars[i:]:
        if t - xt > HORIZON:
            break
        adv, fav = (l, h) if s > 0 else (h, l)
        if g(adv) <= g(sl):
            out, cf, at = "SL", g(sl), t
            break
        if g(fav) >= g(tp):
            out, cf, at = "TP", g(tp), t
            break
        peak = max(peak, g(fav))
        last = (h + l) / 2
    if cf is None:
        cf = g(last)
    rec = dict(m=mech(reason), strat=r["strat"], real=real, cf=cf, out=out, peak=peak)
    rows[rec["m"]].append(rec)
    detail.append(rec)

print(f"{'mechanism':24}{'n':>5}{'saved':>7}{'hurt':>6}{'real $/oz':>11}{'untouched':>11}{'net effect':>12}   untouched outcome TP/SL/open")
tot_r = tot_c = 0
for m, rs in sorted(rows.items(), key=lambda kv: -len(kv[1])):
    saved = sum(1 for x in rs if x["real"] > x["cf"])
    real, cf = sum(x["real"] for x in rs), sum(x["cf"] for x in rs)
    tot_r, tot_c = tot_r + real, tot_c + cf
    oc = {k: sum(1 for x in rs if x["out"] == k) for k in ("TP", "SL", "open")}
    print(f"{m:24}{len(rs):5}{saved:7}{len(rs)-saved:6}{real:11.2f}{cf:11.2f}{real-cf:12.2f}   {oc['TP']}/{oc['SL']}/{oc['open']}")
print(f"{'all protections':24}{sum(map(len, rows.values())):5}{'':13}{tot_r:11.2f}{tot_c:11.2f}{tot_r-tot_c:12.2f}")

print("\npeak drawback by strategy:")
by = defaultdict(list)
for x in rows["peak drawback"]:
    by[x["strat"]].append(x)
for st, rs in sorted(by.items()):
    real, cf = sum(x["real"] for x in rs), sum(x["cf"] for x in rs)
    print(f"  {st:20}{len(rs):4}  real {real:8.2f}  untouched {cf:8.2f}  effect {real-cf:8.2f}  TP {sum(1 for x in rs if x['out']=='TP')} SL {sum(1 for x in rs if x['out']=='SL')}")

pd_ = rows["peak drawback"]
print("\npeak drawback, by what happened next:")
for k in ("TP", "SL", "open"):
    rs = [x for x in pd_ if x["out"] == k]
    if rs:
        print(f"  went to {k:4}: {len(rs):3} trades, bot kept {sum(x['real'] for x in rs):8.2f}, untouched {sum(x['cf'] for x in rs):8.2f}")

"""Gold trend following on 4h bars built from cTrader H1, 2021-2026. Signal at the bar close, position from the next bar.
Cost 0.015% of price per unit of position change. No swap (CFD overnight financing would reduce long returns further).
Usage: python3 -I trend.py /Users/denismgaya/dev/makini/gold-history/H1.csv
"""
import sys
import numpy as np
import pandas as pd

COST = 0.00015
h1 = pd.read_csv(sys.argv[1], header=None, names=["t", "o", "h", "l", "c", "v"])
h1.index = pd.to_datetime(h1["t"], unit="s", utc=True)
b = h1.resample("4h").agg({"o": "first", "h": "max", "l": "min", "c": "last"}).dropna()
b["ret"] = b["c"].pct_change().fillna(0)
BPD = 6  # 4h bars per trading day (approx)


def tsmom(days, shorts=False):
    p = b["c"].pct_change(days * BPD)
    return pd.Series(np.where(p > 0, 1.0, -1.0 if shorts else 0.0), index=b.index)


def donchian(entry, exit_, shorts=False):
    hi = b["h"].rolling(entry * BPD).max().shift(1)
    lo = b["l"].rolling(entry * BPD).min().shift(1)
    xl = b["l"].rolling(exit_ * BPD).min().shift(1)
    xh = b["h"].rolling(exit_ * BPD).max().shift(1)
    pos, out = 0, []
    for c, H, L, XL, XH in zip(b["c"], hi, lo, xl, xh):
        if pos == 1 and c < XL or pos == -1 and c > XH:
            pos = 0
        if pos == 0 and c > H:
            pos = 1
        elif pos == 0 and shorts and c < L:
            pos = -1
        out.append(pos)
    return pd.Series(out, index=b.index, dtype=float)


def vol(pos, target=0.15, cap=2.0):
    v = b["ret"].rolling(30 * BPD).std() * np.sqrt(252 * BPD)
    return pos * (target / v).clip(upper=cap).fillna(0)


def stats(name, pos):
    held = pos.shift(1).fillna(0)
    pnl = (held * b["ret"] - (pos - held).abs().shift(1).fillna(0) * COST)["2021-04-01":]
    eq = (1 + pnl).cumprod()
    yrs = (pnl.index[-1] - pnl.index[0]).days / 365.25
    row = {"strategy": name, "CAGR%": round((eq.iloc[-1] ** (1 / yrs) - 1) * 100, 1),
           "maxDD%": round((eq / eq.cummax() - 1).min() * 100, 1),
           "sharpe": round(pnl.mean() / pnl.std() * np.sqrt(252 * BPD), 2),
           "trades/yr": round((held.diff().abs() > 0).sum() / yrs), "in mkt%": round((held != 0).mean() * 100)}
    row.update({str(y): round(v * 100, 1) for y, v in pnl.groupby(pnl.index.year).apply(lambda r: (1 + r).prod() - 1).items()})
    return row


c = {"buy & hold": pd.Series(1.0, index=b.index)}
for d in (10, 20, 30, 60, 90):
    c[f"tsmom {d}d long"] = tsmom(d)
    c[f"tsmom {d}d L/S"] = tsmom(d, True)
for e, x in ((10, 5), (20, 10), (40, 20), (55, 20)):
    c[f"donchian {e}/{x} long"] = donchian(e, x)
    c[f"donchian {e}/{x} L/S"] = donchian(e, x, True)
c["ensemble tsmom 10-90 long"] = sum(tsmom(d) for d in (10, 20, 30, 60, 90)) / 5
c["ensemble tsmom 10-90 L/S"] = sum(tsmom(d, True) for d in (10, 20, 30, 60, 90)) / 5
c["ensemble tsmom long, vol 15%"] = vol(c["ensemble tsmom 10-90 long"])
c["ensemble tsmom L/S, vol 15%"] = vol(c["ensemble tsmom 10-90 L/S"])
c["buy & hold, vol 15%"] = vol(c["buy & hold"])
pd.set_option("display.width", 250)
pd.set_option("display.max_columns", 30)
print(pd.DataFrame([stats(k, v) for k, v in c.items()]).to_string(index=False))

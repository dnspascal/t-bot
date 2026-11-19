"""Long gold after the daily reopen (18:00 New York), entering a few minutes late to avoid the widest spread.
Cost: 0.015% of price per round trip (~$0.29 at $1,900, ~$0.58 at $3,900). M5 bars, NY time, DST-aware.
Usage: python3 -I asia_open.py /Users/denismgaya/dev/makini/gold-history/M5.csv
"""
import sys
import numpy as np
import pandas as pd

COST = 0.00015
b = pd.read_csv(sys.argv[1], header=None, names=["t", "o", "h", "l", "c", "v"])
ny = pd.to_datetime(b["t"], unit="s", utc=True).dt.tz_convert("America/New_York")
b["day"] = (ny - pd.Timedelta(hours=18)).dt.date  # session day starts at the 18:00 reopen
b["hm"] = ny.dt.strftime("%H:%M")
b["year"] = ny.dt.year
op = b.pivot_table(index="day", columns="hm", values="o", aggfunc="first")
yr = b.groupby("day")["year"].first()

entries = ["18:05", "18:15", "18:30", "19:00"]
exits = ["19:00", "20:00", "21:00", "22:00", "00:00", "02:00"]
rows = []
for e in entries:
    for x in exits:
        if x <= e and x >= "18:00":
            continue
        if e not in op or x not in op:
            continue
        d = op[[e, x]].dropna()
        net = d[x] - d[e] - COST * d[e]
        pct = net / d[e] * 100
        res = {"entry": e, "exit": x}
        pos = 0
        for y, g in pct.groupby(yr.reindex(d.index)):
            res[str(y)] = round(g.mean() * 100, 1)  # basis points per trade
            pos += g.mean() > 0
        res["all bp"] = round(pct.mean() * 100, 1)
        res["t"] = round(pct.mean() / pct.std() * np.sqrt(len(pct)), 1)
        res["win%"] = round((pct > 0).mean() * 100)
        res["yrs+"] = f"{pos}/{len(set(yr))}"
        res["n"] = len(pct)
        rows.append(res)
pd.set_option("display.width", 250)
print("Net return per trade in basis points (1 bp = 0.01%; at $3,900 gold, 1 bp = $0.39/oz)\n")
print(pd.DataFrame(rows).to_string(index=False))

"""Gold drift by hour of day (New York time, DST-aware), per year, 2021-2026, from cTrader H1 bars.
Usage: python3 -I hours.py /Users/denismgaya/dev/makini/gold-history/H1.csv
"""
import sys
import numpy as np
import pandas as pd

b = pd.read_csv(sys.argv[1], header=None, names=["t", "o", "h", "l", "c", "v"])
b["ts"] = pd.to_datetime(b["t"], unit="s", utc=True)
b["ny"] = b["ts"].dt.tz_convert("America/New_York")
b["hour"] = b["ny"].dt.hour
b["year"] = b["ts"].dt.year
b["ret"] = b["c"] - b["o"]  # $ per oz within the hour
b["rng"] = b["h"] - b["l"]

print("Average H1 range ($) by year:", b.groupby("year")["rng"].mean().round(2).to_dict(), "\n")
print("Mean $ move per hour, by New York hour and year (* = |t| > 2 that year)")
years = sorted(b["year"].unique())
print(f"{'NY hour':>8} {'EAT':>5} " + " ".join(f"{y:>9}" for y in years) + f" {'all':>8} {'t all':>6} {'yrs +':>6}")
for h in range(24):
    g = b[b["hour"] == h]
    cells, pos = [], 0
    for y in years:
        x = g[g["year"] == y]["ret"]
        if len(x) < 30:
            cells.append(f"{'':>9}")
            continue
        t = x.mean() / x.std() * np.sqrt(len(x))
        pos += x.mean() > 0
        cells.append(f"{x.mean():8.2f}{'*' if abs(t) > 2 else ' '}")
    x = g["ret"]
    t = x.mean() / x.std() * np.sqrt(len(x))
    print(f"{h:>8} {(h + 8) % 24:>5} " + " ".join(cells) + f" {x.mean():8.2f} {t:6.1f} {pos:>4}/{len(years)}")

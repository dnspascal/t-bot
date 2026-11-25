"""Each exhaustion sign as a filter on trend_follow: would it have blocked the two losing sells of 2026-11-25
(18:25 and 18:30 EAT), and what does it do to trend_follow's 10-year trades?
History H1/M5 is spliced with the bot's own prod candles for the last day. Filters only use bars closed before entry.
Usage: python3 -I filters.py <download dir> <prod H1 csv> <prod M5 csv> <trend_follow trades csv>
"""
import bisect, csv, datetime as dt, statistics as st, sys

def load(path):
    return [(int(r[0]), float(r[1]), float(r[2]), float(r[3]), float(r[4]), float(r[5])) for r in csv.reader(open(path)) if r]
def splice(hist, live):
    cut = live[0][0]
    return [b for b in hist if b[0] < cut] + live
H = splice(load(f"{sys.argv[1]}/H1.csv"), load(sys.argv[2]))
M = splice(load(f"{sys.argv[1]}/M5.csv"), load(sys.argv[3]))
T = [b[0] for b in H]; O = [b[1] for b in H]; HI = [b[2] for b in H]; LO = [b[3] for b in H]; C = [b[4] for b in H]; V = [b[5] for b in H]
N = len(H)

def wilder(closes):
    out = [50.0] * len(closes); g = l = None
    for i in range(1, len(closes)):
        ch = closes[i] - closes[i - 1]
        if g is None: g, l = max(ch, 0), max(-ch, 0)
        else: g += (max(ch, 0) - g) / 14; l += (max(-ch, 0) - l) / 14
        out[i] = 100 - 100 / (1 + g / l) if l else 100
    return out
rsi = wilder(C)
atr = [0.0] * N; a = None
for i in range(1, N):
    tr = max(HI[i] - LO[i], abs(HI[i] - C[i - 1]), abs(LO[i] - C[i - 1]))
    a = tr if a is None else a + (tr - a) / 14; atr[i] = a
e50 = []; e = None
for c in C:
    e = c if e is None else e + (c - e) * 2 / 51; e50.append(e)
MT = [b[0] for b in M]; m5rsi = wilder([b[4] for b in M])

dayhl, sofar = {}, [None] * N
for i in range(N):
    k = dt.datetime.fromtimestamp(T[i], dt.timezone.utc).date()
    h_, l_ = dayhl.get(k, (HI[i], LO[i])); dayhl[k] = (max(h_, HI[i]), min(l_, LO[i])); sofar[i] = dayhl[k]
days = sorted(dayhl); adr = {}
for j, k in enumerate(days):
    prev = [dayhl[x][0] - dayhl[x][1] for x in days[max(0, j - 20):j]]
    adr[k] = st.mean(prev) if prev else None

def signs(t, s):
    i = bisect.bisect_right(T, t - 3600) - 1  # last H1 bar closed by t
    if i < 60: return None
    A = atr[i]; w = range(i - 23, i + 1)
    hi24, lo24 = max(HI[k] for k in w), min(LO[k] for k in w)
    stretched = (C[i] - lo24 >= 3 * A and hi24 - C[i] <= 0.3 * A) if s > 0 else (hi24 - C[i] >= 3 * A and C[i] - lo24 <= 0.3 * A)
    pw = range(i - 47, i - 11)
    pk = max(pw, key=lambda k: HI[k]) if s > 0 else min(pw, key=lambda k: LO[k])
    new_ext = HI[i] > HI[pk] if s > 0 else LO[i] < LO[pk]
    div = new_ext and (rsi[i] < rsi[pk] - 3 if s > 0 else rsi[i] > rsi[pk] + 3)
    vol = V[i] >= 2 * (st.mean(V[i - 20:i]) or 1)
    rg = HI[i] - LO[i]
    wick = rg > 0 and ((HI[i] - C[i]) if s > 0 else (C[i] - LO[i])) / rg >= 0.5
    k = dt.datetime.fromtimestamp(T[i], dt.timezone.utc).date(); a20 = adr.get(k)
    dayx = a20 and (sofar[i][0] - sofar[i][1]) / a20 >= 1.5
    r_dir = rsi[i] if s > 0 else 100 - rsi[i]
    jm = bisect.bisect_right(MT, t - 300) - 1
    m5 = m5rsi[jm] if s > 0 else 100 - m5rsi[jm]
    run = 0
    for q in range(i, i - 24, -1):
        if (C[q] > O[q]) == (s > 0): run += 1
        else: break
    ema = s * (C[i] - e50[i]) / A >= 4
    return {"stretched 3 ATR in 24h": stretched, "RSI divergence": div, "volume >= 2x": vol, "rejection wick >= 50%": wick,
            "day range >= 1.5x ADR": bool(dayx), "H1 RSI >= 80 (sell <= 20)": r_dir >= 80, "M5 RSI >= 85 (sell <= 15)": m5 >= 85,
            "5+ H1 candles in a row": run >= 5, ">= 4 ATR beyond EMA50": ema,
            "_rsi": rsi[i], "_m5": m5rsi[jm], "_ema_atr": (C[i] - e50[i]) / A}

eat = dt.timezone(dt.timedelta(hours=3))
today = [(int(dt.datetime(2026, 11, 25, 18, 25, tzinfo=eat).timestamp()), -1), (int(dt.datetime(2026, 11, 25, 18, 30, tzinfo=eat).timestamp()), -1)]
tf = [signs(t, s) for t, s in today]
print("today's two losing sells (18:25, 18:30 EAT): H1 RSI {:.1f}/{:.1f}, M5 RSI {:.1f}/{:.1f}, EMA50 distance {:+.1f}/{:+.1f} ATR".format(
    tf[0]["_rsi"], tf[1]["_rsi"], tf[0]["_m5"], tf[1]["_m5"], tf[0]["_ema_atr"], tf[1]["_ema_atr"]))

trades = []
for r in csv.reader(open(sys.argv[4])):
    if not r or r[0] not in ("BUY", "SELL"): continue
    s = 1 if r[0] == "BUY" else -1
    f = signs(int(r[1]), s)
    if f: trades.append((dt.datetime.fromtimestamp(int(r[1]), dt.timezone.utc).year, float(r[4]), f))
base = sum(x[1] for x in trades)
by_base = {}
for y, R, _ in trades: by_base[y] = by_base.get(y, 0) + R
print(f"\ntrend_follow 2016-2026: {len(trades)} trades, {base:+.1f}R without any filter\n")
print(f"{'filter (block the entry when present)':32}{'stops 18:25':>12}{'stops 18:30':>12}{'blocked':>9}{'their R':>9}{'avg R':>8}{'R with filter':>14}{'better yrs':>11}")
for name in [k for k in tf[0] if not k.startswith("_")]:
    blk = [x for x in trades if x[2][name]]
    kept = [x for x in trades if not x[2][name]]
    by = {}
    for y, R, _ in kept: by[y] = by.get(y, 0) + R
    better = sum(by.get(y, 0) > by_base[y] for y in by_base)
    print(f"{name:32}{('yes' if tf[0][name] else 'no'):>12}{('yes' if tf[1][name] else 'no'):>12}{len(blk):9}{sum(x[1] for x in blk):+9.1f}"
          f"{(st.mean(x[1] for x in blk) if blk else 0):+8.3f}{sum(x[1] for x in kept):+14.1f}{better:>8}/11")

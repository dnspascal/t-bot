# t-bot

Algorithmic trading bot for forex and commodities via cTrader.

## Stack

- **Go** — core bot, order execution, risk management
- **PostgreSQL + TimescaleDB** — tick and candle storage
- **XGBoost / ONNX** — ML signal filter, trained on 13 months of M5/M15/H1 data
- **Telegram** — trade notifications

## Strategies

| Name | Description |
| ------ | ------------- |
| `sr_bounce` | RSI extreme at M15 S/R level, ML-filtered via XGBoost ONNX model |
| `trend_follow` | EMA + ADX trend continuation |
| `combined` | Runs both; first signal wins |

### Outcome-aware cooldown

Most strategies pause trading a direction after two consecutive "invalidated"
closes in that direction (an SL hit, a time-stop, or the watcher's early-exit
reversal signals — see `internal/strategy/close_reason.go`), so a strategy
doesn't keep re-entering the same losing thesis while the market conditions
that caused it haven't changed. A validated close (TP hit, break-even, peak
drawback) resets the streak. Cooldown length is 60min for H1-regime-gated
strategies, 30min for faster M5/M15 setups:

**60min:** `ema_pullback`, `trend_follow`, `dd_oversold_bounce`,
`dd_ranging_breakout`, `dd_early_breakout`

**30min:** `breakout`, `rsi_reversal`, `session_momentum`, `sr_bounce`,
`session_open`, `regime`

`combined` has no cooldown of its own — it delegates to whichever sub-strategy
actually signals.

## Configuration

Copy `.env.example` and fill in credentials. Key vars:

```env
STRATEGY=combined
CTRADER_SYMBOL=EURUSD
ML_MODEL_DIR=/path/to/models   # directory containing eurusd_model.onnx / xauusd_model.onnx
```

## ML Model

Train locally, deploy as ONNX:

```bash
python3 ml/train.py          # outputs ml/eurusd_model.onnx, ml/xauusd_model.onnx
scp ml/*.onnx server:~/models/
```

## Run

```bash
make migrate-up
make build
make run
```

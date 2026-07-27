package risk

import (
	"fmt"
	"time"
)


type Manager struct {
	riskPercent      float64
	maxDailyLossPct  float64 

	dailyPnL float64 
	dayStart time.Time

	unitsPerMicroLot    int64
	minVolume           int64
	maxVolume           int64
	pipValuePerMicroLot float64 
}

func New(riskPercent, maxDailyLossPct float64) *Manager {
	return &Manager{
		riskPercent:         riskPercent,
		maxDailyLossPct:     maxDailyLossPct,
		dayStart:            today(),
		unitsPerMicroLot:    1000,
		minVolume:           1000,
		maxVolume:           5_000_000,
		pipValuePerMicroLot: 0.10,
	}
}


func (m *Manager) SetVolumeConfig(unitsPerMicroLot, minVolume, maxVolume int64, pipValue float64) {
	m.unitsPerMicroLot = unitsPerMicroLot
	m.minVolume = minVolume
	m.maxVolume = maxVolume
	m.pipValuePerMicroLot = pipValue
}

var dsmLocation, _ = time.LoadLocation("Africa/Dar_es_Salaam")


func (m *Manager) PositionSize(balance, stopLossPips float64) (int64, error) {
	if stopLossPips < 3 {
		return 0, fmt.Errorf("stop loss too tight: %.1f pips (minimum 3)", stopLossPips)
	}

	riskAmount := balance * (m.riskPercent / 100)

	microLots := riskAmount / (stopLossPips * m.pipValuePerMicroLot)

	volume := int64(microLots * float64(m.unitsPerMicroLot))
	if m.unitsPerMicroLot > 0 {
		volume = (volume / m.unitsPerMicroLot) * m.unitsPerMicroLot
	}
	volume = max(m.minVolume, min(volume, m.maxVolume))

	return volume, nil
}

func (m *Manager) PositionSizeForTier(balance, stopLossPips float64, tier int) (int64, error) {
	base, err := m.PositionSize(balance, stopLossPips)
	if err != nil {
		return 0, err
	}
	return min(base*int64(tier+1), m.maxVolume), nil
}


func (m *Manager) dailyLimit(balance float64) float64 {
	return balance * (m.maxDailyLossPct / 100)
}


func (m *Manager) RecordTrade(realized float64) {
	m.resetDayIfNeeded()
	m.dailyPnL += realized
}

func (m *Manager) RestorePnL(netPnL float64) {
	m.dailyPnL = netPnL
}

func (m *Manager) CanTrade(balance float64) bool {
	m.resetDayIfNeeded()
	return m.dailyPnL > -m.dailyLimit(balance)
}

func (m *Manager) DailyLoss() float64 {
	m.resetDayIfNeeded()
	if m.dailyPnL >= 0 {
		return 0
	}
	return -m.dailyPnL
}

func (m *Manager) MaxDailyLossPct() float64 {
	return m.maxDailyLossPct
}

func (m *Manager) resetDayIfNeeded() {
	if today().After(m.dayStart) {
		m.dailyPnL = 0
		m.dayStart = today()
	}
}

func today() time.Time {
	now := time.Now().In(dsmLocation)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, dsmLocation)
}

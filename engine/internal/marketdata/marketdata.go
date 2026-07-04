// Package marketdata aggregates the trade stream into OHLCV candles and
// rolling per-instrument session statistics.
package marketdata

import (
	"sync"
	"time"

	"github.com/alim596/meridian/internal/engine"
)

type Interval struct {
	Name string
	Dur  time.Duration
}

var Intervals = []Interval{
	{"1s", time.Second},
	{"5s", 5 * time.Second},
	{"1m", time.Minute},
}

func IntervalByName(name string) (Interval, bool) {
	for _, iv := range Intervals {
		if iv.Name == name {
			return iv, true
		}
	}
	return Interval{}, false
}

type Candle struct {
	T int64 `json:"t"` // bucket start, unix seconds
	O int64 `json:"o"`
	H int64 `json:"h"`
	L int64 `json:"l"`
	C int64 `json:"c"`
	V int64 `json:"v"`
}

// maxCandles caps history per (instrument, interval).
const maxCandles = 3000

type series struct {
	candles []Candle
}

func (s *series) add(bucket, price, qty int64) {
	n := len(s.candles)
	if n > 0 && s.candles[n-1].T == bucket {
		c := &s.candles[n-1]
		if price > c.H {
			c.H = price
		}
		if price < c.L {
			c.L = price
		}
		c.C = price
		c.V += qty
		return
	}
	s.candles = append(s.candles, Candle{T: bucket, O: price, H: price, L: price, C: price, V: qty})
	if len(s.candles) > maxCandles {
		s.candles = s.candles[len(s.candles)-maxCandles:]
	}
}

// Stats is a session summary for one instrument.
type Stats struct {
	Symbol     string  `json:"symbol"`
	Last       int64   `json:"last"`
	Open       int64   `json:"open"` // first trade of the session
	High       int64   `json:"high"`
	Low        int64   `json:"low"`
	Volume     int64   `json:"volume"`
	TradeCount int64   `json:"tradeCount"`
	ChangePct  float64 `json:"changePct"`
}

type MarketData struct {
	mu     sync.Mutex
	series map[string]map[string]*series // symbol -> interval -> series
	stats  map[string]*Stats
}

func New() *MarketData {
	return &MarketData{
		series: make(map[string]map[string]*series),
		stats:  make(map[string]*Stats),
	}
}

// Seed registers an instrument so stats exist before the first trade.
func (m *MarketData) Seed(symbol string, initPrice int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats[symbol] = &Stats{
		Symbol: symbol, Last: initPrice, Open: initPrice,
		High: initPrice, Low: initPrice,
	}
}

// OnEvent consumes the event stream; only trades matter here.
func (m *MarketData) OnEvent(ev engine.Event) {
	if ev.Kind != engine.EvTrade || ev.Trade == nil {
		return
	}
	price, qty := ev.Trade.Price, ev.Trade.Qty
	ts := time.Unix(0, ev.TS)

	m.mu.Lock()
	defer m.mu.Unlock()

	bySym := m.series[ev.Instrument]
	if bySym == nil {
		bySym = make(map[string]*series)
		m.series[ev.Instrument] = bySym
	}
	for _, iv := range Intervals {
		s := bySym[iv.Name]
		if s == nil {
			s = &series{}
			bySym[iv.Name] = s
		}
		bucket := ts.Truncate(iv.Dur).Unix()
		s.add(bucket, price, qty)
	}

	st := m.stats[ev.Instrument]
	if st == nil {
		st = &Stats{Symbol: ev.Instrument, Open: price, High: price, Low: price}
		m.stats[ev.Instrument] = st
	}
	if st.TradeCount == 0 && st.Volume == 0 {
		// first real trade defines the session open if seeded
		st.Open = price
		st.High, st.Low = price, price
	}
	st.Last = price
	if price > st.High {
		st.High = price
	}
	if price < st.Low {
		st.Low = price
	}
	st.Volume += qty
	st.TradeCount++
	if st.Open != 0 {
		st.ChangePct = 100 * float64(price-st.Open) / float64(st.Open)
	}
}

// Candles returns up to limit most recent candles for symbol/interval.
func (m *MarketData) Candles(symbol, interval string, limit int) []Candle {
	m.mu.Lock()
	defer m.mu.Unlock()
	bySym := m.series[symbol]
	if bySym == nil {
		return []Candle{}
	}
	s := bySym[interval]
	if s == nil {
		return []Candle{}
	}
	c := s.candles
	if limit > 0 && len(c) > limit {
		c = c[len(c)-limit:]
	}
	return append([]Candle{}, c...)
}

// Stats returns the session stats for one symbol.
func (m *MarketData) StatsFor(symbol string) (Stats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.stats[symbol]
	if !ok {
		return Stats{}, false
	}
	return *st, true
}

// AllStats returns stats for every seeded instrument.
func (m *MarketData) AllStats() []Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Stats, 0, len(m.stats))
	for _, st := range m.stats {
		out = append(out, *st)
	}
	return out
}

// Marks returns last prices for mark-to-market.
func (m *MarketData) Marks() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.stats))
	for sym, st := range m.stats {
		out[sym] = st.Last
	}
	return out
}

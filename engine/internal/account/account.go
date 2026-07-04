// Package account tracks cash, positions and P&L per account, applies
// pre-trade risk checks, and records fills for the private API.
//
// The manager consumes the sequenced event stream rather than sharing state
// with the engines: risk checks are therefore optimistic (state may be a few
// events stale under load), which is an explicit, documented trade-off —
// the alternative is a synchronous cross-instrument lock inside the matching
// path, which real venues avoid too.
package account

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/orderbook"
)

const (
	// StartingCash for new trading sessions, in ticks (cents): $250,000.
	StartingCash int64 = 25_000_000
	// MaxPosition bounds absolute position per instrument, in lots.
	MaxPosition int64 = 20_000
	maxFillsKept       = 500
)

type Position struct {
	Qty      int64   `json:"qty"`      // signed; negative = short
	AvgCost  float64 `json:"avgCost"`  // ticks, average entry price of open qty
	Realized float64 `json:"realized"` // ticks*lots, realized P&L
}

type Fill struct {
	ID         uint64 `json:"id"` // manager-global, monotonic
	Instrument string `json:"instrument"`
	OrderID    uint64 `json:"orderId"`
	Side       string `json:"side"`
	Price      int64  `json:"price"`
	Qty        int64  `json:"qty"`
	Liquidity  string `json:"liquidity"` // maker | taker
	TS         int64  `json:"ts"`
}

// OpenOrder is a resting order tracked for the blotter, reconstructed from
// the event stream (accepted -> reduced by trades -> removed on cancel/fill).
type OpenOrder struct {
	Instrument string `json:"instrument"`
	OrderID    uint64 `json:"orderId"`
	Side       string `json:"side"`
	Price      int64  `json:"price"`
	Qty        int64  `json:"qty"`
	Remaining  int64  `json:"remaining"`
	TS         int64  `json:"ts"`
}

func orderKey(instrument string, id uint64) string {
	return instrument + "/" + fmt.Sprint(id)
}

type Account struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Cash      int64                `json:"cash"` // ticks
	Sim       bool                 `json:"sim"`  // simulation account: skip risk checks
	Positions map[string]*Position `json:"positions"`
	fills     []Fill
	open      map[string]*OpenOrder
}

type Manager struct {
	mu       sync.Mutex
	accounts map[string]*Account
	keys     map[string]string // apiKey -> accountID
	fillSeq  uint64
}

func NewManager() *Manager {
	return &Manager{
		accounts: make(map[string]*Account),
		keys:     make(map[string]string),
	}
}

func randomID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

// CreateSession provisions a fresh trading account and returns its API key.
func (m *Manager) CreateSession(name string) (accountID, apiKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	accountID = randomID("acct_", 6)
	apiKey = randomID("mk_", 18)
	if name == "" {
		name = "Trader " + accountID[len(accountID)-4:]
	}
	m.accounts[accountID] = &Account{
		ID: accountID, Name: name, Cash: StartingCash,
		Positions: make(map[string]*Position),
		open:      make(map[string]*OpenOrder),
	}
	m.keys[apiKey] = accountID
	return accountID, apiKey
}

// CreateSimAccount provisions an internal account for simulation agents.
func (m *Manager) CreateSimAccount(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := randomID("sim_", 6)
	m.accounts[id] = &Account{
		ID: id, Name: name, Cash: 0, Sim: true,
		Positions: make(map[string]*Position),
		open:      make(map[string]*OpenOrder),
	}
	return id
}

// Resolve maps an API key to an account ID.
func (m *Manager) Resolve(apiKey string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.keys[apiKey]
	return id, ok
}

// CheckOrder implements engine.RiskChecker.
func (m *Manager) CheckOrder(accountID, instrument string, side orderbook.Side, typ orderbook.OrderType, price, qty, refPrice int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return fmt.Errorf("unknown account")
	}
	if a.Sim {
		return nil
	}

	pos := int64(0)
	if p, ok := a.Positions[instrument]; ok {
		pos = p.Qty
	}
	// Position limit on the post-fill worst case.
	worst := pos
	if side == orderbook.Bid {
		worst += qty
	} else {
		worst -= qty
	}
	if worst > MaxPosition || worst < -MaxPosition {
		return fmt.Errorf("position limit exceeded (max %d lots)", MaxPosition)
	}

	// Buying-power check for buys, at limit price (or reference price for
	// market orders). Deliberately does not reserve notional for open
	// orders — see package comment.
	if side == orderbook.Bid {
		px := price
		if typ == orderbook.Market {
			px = refPrice
		}
		if notional := px * qty; notional > a.Cash {
			return fmt.Errorf("insufficient buying power")
		}
	}
	return nil
}

// OnEvent consumes the sequenced event stream: trades settle cash and
// positions; order lifecycle events maintain each account's open orders.
func (m *Manager) OnEvent(ev engine.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch ev.Kind {
	case engine.EvTrade:
		t := ev.Trade
		taker, maker := m.accounts[t.TakerAccount], m.accounts[t.MakerAccount]
		takerBuys := t.TakerSide == "buy"
		if taker != nil {
			m.settle(taker, ev, t.TakerOrderID, takerBuys, "taker")
		}
		if maker != nil {
			m.settle(maker, ev, t.MakerOrderID, !takerBuys, "maker")
			// reduce the maker's resting order
			key := orderKey(ev.Instrument, t.MakerOrderID)
			if oo := maker.open[key]; oo != nil {
				oo.Remaining -= t.Qty
				if oo.Remaining <= 0 {
					delete(maker.open, key)
				}
			}
		}
	case engine.EvAccepted:
		o := ev.Order
		a := m.accounts[o.Account]
		if a != nil && o.Remaining > 0 && o.Type == "limit" && o.TIF == "gtc" {
			a.open[orderKey(ev.Instrument, o.ID)] = &OpenOrder{
				Instrument: ev.Instrument, OrderID: o.ID, Side: o.Side,
				Price: o.Price, Qty: o.Qty, Remaining: o.Remaining, TS: ev.TS,
			}
		}
	case engine.EvCanceled:
		o := ev.Order
		if a := m.accounts[o.Account]; a != nil {
			delete(a.open, orderKey(ev.Instrument, o.ID))
		}
	}
}

// OpenOrders lists an account's resting orders.
func (m *Manager) OpenOrders(accountID string) []OpenOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return nil
	}
	out := make([]OpenOrder, 0, len(a.open))
	for _, oo := range a.open {
		out = append(out, *oo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out
}

// settle applies one fill to one account: cash, position, realized P&L.
func (m *Manager) settle(a *Account, ev engine.Event, orderID uint64, isBuy bool, liquidity string) {
	t := ev.Trade
	signedQty := t.Qty
	side := "buy"
	if !isBuy {
		signedQty = -t.Qty
		side = "sell"
	}
	a.Cash -= signedQty * t.Price

	p := a.Positions[ev.Instrument]
	if p == nil {
		p = &Position{}
		a.Positions[ev.Instrument] = p
	}
	oldQty := p.Qty
	newQty := oldQty + signedQty
	switch {
	case oldQty == 0 || (oldQty > 0) == (signedQty > 0):
		// opening or adding: blend average cost
		p.AvgCost = (p.AvgCost*float64(abs(oldQty)) + float64(t.Price)*float64(abs(signedQty))) / float64(abs(oldQty)+abs(signedQty))
	case abs(signedQty) <= abs(oldQty):
		// reducing: realize P&L on the closed quantity
		closed := float64(abs(signedQty))
		if oldQty > 0 {
			p.Realized += (float64(t.Price) - p.AvgCost) * closed
		} else {
			p.Realized += (p.AvgCost - float64(t.Price)) * closed
		}
		if newQty == 0 {
			p.AvgCost = 0
		}
	default:
		// crossing through zero: realize on the full old position, open the rest
		closed := float64(abs(oldQty))
		if oldQty > 0 {
			p.Realized += (float64(t.Price) - p.AvgCost) * closed
		} else {
			p.Realized += (p.AvgCost - float64(t.Price)) * closed
		}
		p.AvgCost = float64(t.Price)
	}
	p.Qty = newQty

	m.fillSeq++
	a.fills = append(a.fills, Fill{
		ID: m.fillSeq, Instrument: ev.Instrument, OrderID: orderID,
		Side: side, Price: t.Price, Qty: t.Qty, Liquidity: liquidity, TS: ev.TS,
	})
	if len(a.fills) > maxFillsKept {
		a.fills = a.fills[len(a.fills)-maxFillsKept:]
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// View is a JSON-safe snapshot of an account with mark-to-market equity.
type View struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Cash         int64               `json:"cash"`
	Equity       float64             `json:"equity"`
	Positions    map[string]PosView  `json:"positions"`
}

type PosView struct {
	Qty        int64   `json:"qty"`
	AvgCost    float64 `json:"avgCost"`
	Realized   float64 `json:"realized"`
	Unrealized float64 `json:"unrealized"`
	Mark       int64   `json:"mark"`
}

// Snapshot returns the account marked against the given last prices.
func (m *Manager) Snapshot(accountID string, marks map[string]int64) (View, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return View{}, false
	}
	v := View{ID: a.ID, Name: a.Name, Cash: a.Cash, Positions: make(map[string]PosView)}
	v.Equity = float64(a.Cash)
	for sym, p := range a.Positions {
		mark := marks[sym]
		unreal := 0.0
		if p.Qty != 0 {
			if p.Qty > 0 {
				unreal = (float64(mark) - p.AvgCost) * float64(p.Qty)
			} else {
				unreal = (p.AvgCost - float64(mark)) * float64(-p.Qty)
			}
		}
		v.Positions[sym] = PosView{
			Qty: p.Qty, AvgCost: p.AvgCost, Realized: p.Realized,
			Unrealized: unreal, Mark: mark,
		}
		v.Equity += float64(mark) * float64(p.Qty)
	}
	return v, true
}

// PositionQty returns the signed position an account holds in an
// instrument; used by simulation agents for inventory skew.
func (m *Manager) PositionQty(accountID, instrument string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return 0
	}
	if p, ok := a.Positions[instrument]; ok {
		return p.Qty
	}
	return 0
}

// Fills returns the account's most recent fills with seq > since.
func (m *Manager) Fills(accountID string, since uint64) []Fill {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return nil
	}
	out := []Fill{}
	for _, f := range a.fills {
		if f.ID > since {
			out = append(out, f)
		}
	}
	return out
}

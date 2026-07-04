// Package bots lets any user deploy algorithmic strategies against the live
// market with one click. Each bot gets its own risk-checked account (same
// $250k, same position limits, same rejections as a human — no privileges),
// its own goroutine, and a leaderboard entry. Three strategies ship:
//
//   - momentum:  chases short-term drift; buys strength, sells weakness
//   - meanrev:   fades moves; sells strength, buys weakness
//   - maker:     quotes both sides around the mid and earns the spread,
//                skewing quotes against inventory
//
// The point is pedagogical as much as technical: deploy a momentum bot and
// a mean-reversion bot on the same instrument and watch them take each
// other's money as regimes shift.
package bots

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/alim596/meridian/internal/account"
	"github.com/alim596/meridian/internal/engine"
	"github.com/alim596/meridian/internal/marketdata"
	"github.com/alim596/meridian/internal/orderbook"
)

const (
	MaxBotsPerOwner = 3
	clipSize        = 40   // lots per aggressive order
	makerSize       = 90   // lots per quote
	positionCap     = 2500 // lots, bot self-imposed (tighter than exchange limit)
)

var Strategies = []string{"momentum", "meanrev", "maker"}

func validStrategy(s string) bool {
	for _, v := range Strategies {
		if v == s {
			return true
		}
	}
	return false
}

type Bot struct {
	ID        string `json:"id"`
	Owner     string `json:"-"`
	Name      string `json:"name"`
	Strategy  string `json:"strategy"`
	Symbol    string `json:"symbol"`
	AccountID string `json:"accountId"`
	CreatedAt int64  `json:"createdAt"`
	Running   bool   `json:"running"`
	cancel    context.CancelFunc
}

type Manager struct {
	mu      sync.Mutex
	bots    map[string]*Bot
	perOwn  map[string]int
	engines map[string]*engine.Engine
	acct    *account.Manager
	md      *marketdata.MarketData
	ctx     context.Context
	nextID  int64
}

func NewManager(ctx context.Context, engines map[string]*engine.Engine, acct *account.Manager, md *marketdata.MarketData) *Manager {
	return &Manager{
		bots: make(map[string]*Bot), perOwn: make(map[string]int),
		engines: engines, acct: acct, md: md, ctx: ctx,
	}
}

// Deploy validates, provisions the bot account, and starts the strategy.
func (m *Manager) Deploy(owner, symbol, strategy, name string) (*Bot, error) {
	if !validStrategy(strategy) {
		return nil, fmt.Errorf("unknown strategy %q (want momentum, meanrev, or maker)", strategy)
	}
	eng, ok := m.engines[symbol]
	if !ok {
		return nil, fmt.Errorf("unknown instrument %q", symbol)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.perOwn[owner] >= MaxBotsPerOwner {
		return nil, fmt.Errorf("bot limit reached (%d per account)", MaxBotsPerOwner)
	}
	m.nextID++
	id := fmt.Sprintf("bot-%d", m.nextID)
	if name == "" {
		name = fmt.Sprintf("%s·%s", symbol, strategy)
	}
	acctID := m.acct.CreateBotAccount(name)

	ctx, cancel := context.WithCancel(m.ctx)
	b := &Bot{
		ID: id, Owner: owner, Name: name, Strategy: strategy, Symbol: symbol,
		AccountID: acctID, CreatedAt: time.Now().UnixMilli(), Running: true,
		cancel: cancel,
	}
	m.bots[id] = b
	m.perOwn[owner]++

	go m.run(ctx, b, eng)
	return b, nil
}

// Stop halts a bot's strategy loop. Its account (and P&L) remains on the
// leaderboard — the record stands.
func (m *Manager) Stop(owner, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bots[id]
	if !ok || b.Owner != owner {
		return fmt.Errorf("bot not found")
	}
	if b.Running {
		b.cancel()
		b.Running = false
		m.perOwn[owner]--
	}
	return nil
}

// View combines bot metadata with live account economics.
type View struct {
	Bot
	Equity float64 `json:"equity"`
	PnL    float64 `json:"pnl"`
}

func (m *Manager) List(owner string, marks map[string]int64) []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []View{}
	for _, b := range m.bots {
		if b.Owner != owner {
			continue
		}
		v := View{Bot: *b}
		if snap, ok := m.acct.Snapshot(b.AccountID, marks); ok {
			v.Equity = snap.Equity
			v.PnL = snap.Equity - float64(account.StartingCash)
		}
		out = append(out, v)
	}
	return out
}

// ---- strategy loops ----

func (m *Manager) run(ctx context.Context, b *Bot, eng *engine.Engine) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var quoted []uint64 // maker's live quotes

	defer func() {
		// leave the book clean on shutdown
		for _, id := range quoted {
			eng.Cancel(engine.CancelCmd{OrderID: id, Account: b.AccountID})
		}
	}()

	period := time.Duration(750+rng.Intn(500)) * time.Millisecond
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			switch b.Strategy {
			case "momentum":
				m.stepDirectional(b, eng, +1)
			case "meanrev":
				m.stepDirectional(b, eng, -1)
			case "maker":
				quoted = m.stepMaker(b, eng, quoted, rng)
			}
		}
	}
}

// stepDirectional implements momentum (dir=+1) and mean reversion (dir=-1):
// measure drift over the last ~8 seconds of 1s candles and trade market
// orders in (or against) its direction past a threshold.
func (m *Manager) stepDirectional(b *Bot, eng *engine.Engine, dir int) {
	candles := m.md.Candles(b.Symbol, "1s", 9)
	if len(candles) < 5 {
		return
	}
	first, last := candles[0], candles[len(candles)-1]
	if first.O == 0 {
		return
	}
	ret := float64(last.C-first.O) / float64(first.O)
	threshold := 0.0012
	if dir < 0 {
		threshold = 0.0022 // fade only meaningful moves
	}
	var side orderbook.Side
	switch {
	case ret > threshold:
		if dir > 0 {
			side = orderbook.Bid
		} else {
			side = orderbook.Ask
		}
	case ret < -threshold:
		if dir > 0 {
			side = orderbook.Ask
		} else {
			side = orderbook.Bid
		}
	default:
		return
	}
	// self-imposed inventory cap, tighter than the exchange limit
	pos := m.acct.PositionQty(b.AccountID, b.Symbol)
	if (side == orderbook.Bid && pos >= positionCap) || (side == orderbook.Ask && pos <= -positionCap) {
		return
	}
	eng.Submit(engine.SubmitCmd{
		Account: b.AccountID, Side: side, Type: orderbook.Market,
		TIF: orderbook.IOC, Qty: clipSize,
	})
}

// stepMaker cancels last cycle's quotes and re-quotes both sides of the
// mid, skewed against inventory.
func (m *Manager) stepMaker(b *Bot, eng *engine.Engine, quoted []uint64, rng *rand.Rand) []uint64 {
	for _, id := range quoted {
		eng.Cancel(engine.CancelCmd{OrderID: id, Account: b.AccountID})
	}
	quoted = quoted[:0]

	snap := eng.Snapshot(1)
	if len(snap.Bids) == 0 || len(snap.Asks) == 0 || snap.HaltedUntil != 0 {
		return quoted
	}
	mid := (snap.Bids[0].Price + snap.Asks[0].Price) / 2
	half := maxI64(3, mid*9/10000) // ~9bps, a touch wider than the house
	inv := m.acct.PositionQty(b.AccountID, b.Symbol)
	skew := -inv / 60

	size := int64(makerSize + rng.Intn(40))
	for _, q := range []struct {
		side  orderbook.Side
		price int64
	}{
		{orderbook.Bid, mid - half + skew},
		{orderbook.Ask, mid + half + skew},
	} {
		if q.price <= 0 {
			continue
		}
		resp := make(chan engine.SubmitResp, 1)
		eng.Submit(engine.SubmitCmd{
			Account: b.AccountID, Side: q.side, Type: orderbook.Limit,
			TIF: orderbook.GTC, Price: q.price, Qty: size, Resp: resp,
		})
		if r := <-resp; r.Status == "accepted" && r.Resting {
			quoted = append(quoted, r.OrderID)
		}
	}
	return quoted
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
